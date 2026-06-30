package overlay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/open-edge-platform/image-composer-tool/internal/config"
	"github.com/open-edge-platform/image-composer-tool/internal/config/manifest"
	"github.com/open-edge-platform/image-composer-tool/internal/ospackage"
	"github.com/open-edge-platform/image-composer-tool/internal/utils/system"
)

// Builder drives an overlay-mode image build across the provider's three phases
// (preprocess, build, postprocess) while keeping a SINGLE baseline mount lifecycle
// open for their whole duration.
//
// The overlay stage primitives (Ingestor.WithBaseline, Inspector.WithMountedLayout)
// are closure-scoped: they tear their mounts down when their callback returns. That
// is the right shape for a single-shot caller, but the provider pipeline invokes
// PreProcess, BuildImage and PostProcess as separate calls, so the baseline must
// stay attached and mounted from preprocess through build and only be released in
// postprocess (or during failure unwind). Builder owns that explicit lifecycle: it
// acquires the loop device and mounts in Preprocess, reuses them in Build, and runs
// the cleanup chain (unmount then detach) in Postprocess regardless of outcome.
//
// Builder never mutates the user-provided baseline source (Ingestor copies it
// first), it is strictly additive (only ResolutionPlan.ToInstall is installed), and
// it never modifies the installed bootloader binary or the ESP (mounted read-only);
// boot regeneration is currently restricted to the initramfs.
type Builder struct {
	template  *config.ImageTemplate
	ingestor  *Ingestor
	inspector *Inspector

	// State populated as the phases run; each is nil until its phase produces it.
	// Only the cross-phase state is retained: the inspected inventory and install
	// result are consumed within the phase that produces them, so they are local.
	ctx    *Context         // loop device + workspace baseline copy
	layout *Layout          // mounted partition layout
	info   *BaselineInfo    // detected baseline OS/arch/package manager
	plan   *ResolutionPlan  // resolved additive package plan
	report *PreflightReport // preflight policy gate result

	// mountTeardown unmounts the layout (reverse order). It is set by Preprocess
	// and run once by Postprocess; nil before mounts exist or after teardown.
	mountTeardown func() error
	// preprocessed and built track how far the pipeline got, so Postprocess only
	// finalizes artifacts on a fully successful build and always runs cleanup.
	preprocessed bool
	built        bool
}

// Builder-stage indirection seams over the impure overlay stages so the phase
// orchestration is unit-testable without root, loop devices, mounts, or network.
// Tests override these; production uses the real stage functions.
var (
	builderAcquire     = func(ing *Ingestor) (*Context, error) { return ing.acquire() }
	builderMountLayout = func(insp *Inspector, loopDev string) (*Layout, func() error, error) {
		return insp.MountLayout(loopDev)
	}
	builderDetach      = func(ing *Ingestor, ctx *Context) error { return ing.detach(ctx) }
	builderDetectFn    = DetectBaseline
	builderResolveFn   = ResolveOverlayPackages
	builderPreflightFn = Preflight
	builderInstallFn   = InstallOverlayPackages
	builderRegenBootFn = RegenerateBoot
	builderResizeFn    = ResizeBaseline
	builderSBOMFn      = generateOverlaySBOM
	builderEmitFn      = emitOverlayArtifact
)

// NewBuilder constructs an overlay Builder for an overlay-mode template. It returns
// an error when the template is not in overlay mode or is missing its baseline
// source (the same gate as NewIngestor), so a create-mode build never reaches here.
func NewBuilder(template *config.ImageTemplate) (*Builder, error) {
	ingestor, err := NewIngestor(template)
	if err != nil {
		return nil, err
	}
	return &Builder{
		template:  template,
		ingestor:  ingestor,
		inspector: NewInspector(ingestor.workDir),
	}, nil
}

// Preprocess runs the overlay preprocess phase: it copies the baseline into the
// workspace, attaches it to a loop device, mounts the layout, inspects the baseline
// to extract its metadata and package inventory, resolves the requested overlay
// packages, and runs the preflight policy gate.
//
// The loop device and mounts it establishes are deliberately left open for the
// Build phase; on any error here it unwinds whatever it set up so no loop device or
// mount leaks, and a later Postprocess call becomes a no-op cleanup.
func (b *Builder) Preprocess() (err error) {
	if b.preprocessed {
		return fmt.Errorf("overlay build: Preprocess already ran")
	}

	// If anything below fails, unwind the partial mount/loop state immediately so
	// the lifecycle is not left half-open between phases.
	defer func() {
		if err != nil {
			b.cleanup()
		}
	}()

	ctx, err := builderAcquire(b.ingestor)
	if err != nil {
		return fmt.Errorf("overlay preprocess: failed to acquire baseline: %w", err)
	}
	b.ctx = ctx

	layout, teardown, err := builderMountLayout(b.inspector, ctx.LoopDevPath)
	if err != nil {
		return fmt.Errorf("overlay preprocess: failed to mount baseline layout: %w", err)
	}
	b.layout = layout
	b.mountTeardown = teardown

	info, baseline, err := builderDetectFn(layout.RootMount, b.template.Target)
	if err != nil {
		return fmt.Errorf("overlay preprocess: failed to inspect baseline: %w", err)
	}
	b.info = info

	plan, err := builderResolveFn(b.template, info, baseline)
	if err != nil {
		return fmt.Errorf("overlay preprocess: dependency resolution failed: %w", err)
	}
	b.plan = plan

	report, err := builderPreflightFn(info, baseline, plan, b.template.OverlayPolicy)
	if err != nil {
		// Preflight returns the report alongside a blocked error; retain it for
		// diagnostics even though installation will not proceed.
		b.report = report
		return fmt.Errorf("overlay preprocess: preflight gate blocked the build: %w", err)
	}
	b.report = report

	b.preprocessed = true
	return nil
}

// Build runs the overlay build phase against the already-mounted baseline: it
// installs the approved package plan, regenerates the initramfs for any added
// packages (never the bootloader), and performs an optional grow-only resize.
//
// It requires Preprocess to have succeeded; the mount lifecycle opened there is
// reused here and is not torn down until Postprocess.
func (b *Builder) Build() error {
	if !b.preprocessed {
		return fmt.Errorf("overlay build: Build called before a successful Preprocess")
	}
	if b.built {
		return fmt.Errorf("overlay build: Build already ran")
	}

	installed, err := builderInstallFn(b.info, b.layout.RootMount, b.plan, b.report)
	if err != nil {
		return fmt.Errorf("overlay build: package installation failed: %w", err)
	}

	if err := builderRegenBootFn(b.info, b.layout.RootMount, installed); err != nil {
		return fmt.Errorf("overlay build: boot regeneration failed: %w", err)
	}

	if err := builderResizeFn(b.template, b.ctx, b.layout); err != nil {
		return fmt.Errorf("overlay build: resize failed: %w", err)
	}

	b.built = true
	return nil
}

// Postprocess finalizes the build and ALWAYS runs the cleanup chain. On a fully
// successful build (buildErr nil and Build completed) it embeds the overlay SBOM
// into the baseline while it is still mounted, then — after unmounting and
// detaching — emits the modified baseline as the final RAW artifact. On any failure
// it skips finalization but still unmounts and detaches, so a stage failure unwinds
// the whole lifecycle.
//
// buildErr is the error (if any) from the preceding phases; it is threaded in so a
// failed build still triggers the full cleanup chain.
func (b *Builder) Postprocess(buildErr error) error {
	// Cleanup (unmount + detach) must run no matter what, so defer it before any
	// fallible finalization work below.
	defer b.cleanup()

	if buildErr != nil || !b.built {
		// A failed or incomplete build: nothing to finalize, just clean up.
		return nil
	}

	// Embed the overlay SBOM into the baseline while the root is still mounted.
	if err := builderSBOMFn(b.info, b.layout.RootMount, b.plan); err != nil {
		return fmt.Errorf("overlay postprocess: SBOM generation failed: %w", err)
	}

	// Release the mounts and loop device before emitting the artifact: the final
	// image is the modified backing file, which must no longer be in use.
	version := b.imageVersion()
	if cerr := b.cleanupOnce(); cerr != nil {
		return fmt.Errorf("overlay postprocess: failed to release baseline before emit: %w", cerr)
	}

	artifact, err := builderEmitFn(b.template, b.ctx.BaselineCopyPath, version)
	if err != nil {
		return fmt.Errorf("overlay postprocess: failed to emit image artifact: %w", err)
	}
	log.Infof("Overlay build complete: emitted %s", artifact)
	return nil
}

// cleanup runs the mount/loop teardown chain. It is idempotent: the mount teardown
// clears itself after running and detach is a no-op once the loop device is gone,
// so Postprocess's deferred cleanup is harmless after an explicit cleanupOnce.
func (b *Builder) cleanup() {
	if err := b.cleanupOnce(); err != nil {
		// Already logged in detail by the teardown; record at warn for the unwind path.
		log.Warnf("Overlay cleanup: %v", err)
	}
}

// cleanupOnce unmounts the layout (reverse order) and detaches the loop device,
// returning the combined unmount and detach errors (via errors.Join, so both are
// surfaced rather than silently dropping the detach failure). A leaked loop
// device is thereby never mistaken for a clean build. It is safe to call
// multiple times.
func (b *Builder) cleanupOnce() error {
	var umountErr, detachErr error
	if b.mountTeardown != nil {
		umountErr = b.mountTeardown()
		b.mountTeardown = nil
	}
	if b.ctx != nil && b.ctx.LoopDevPath != "" {
		detachErr = builderDetach(b.ingestor, b.ctx)
		// Clear the loop path so a second cleanup does not attempt to detach an
		// already-released device (this guard makes cleanupOnce idempotent).
		b.ctx.LoopDevPath = ""
	}
	return errors.Join(umountErr, detachErr)
}

// imageVersion derives the artifact version tag. The overlaid image fundamentally
// carries the baseline's version, so the detected baseline VERSION_ID is preferred;
// a template-specified image version overrides it, and "overlay" is the last resort.
func (b *Builder) imageVersion() string {
	if v := strings.TrimSpace(b.template.Image.Version); v != "" {
		return v
	}
	if b.info != nil {
		if v := strings.TrimSpace(b.info.Version); v != "" {
			return v
		}
	}
	return "overlay"
}

// generateOverlaySBOM writes an SPDX SBOM of the packages the overlay ADDED (the
// additive ToInstall set) and embeds it into the baseline filesystem at the
// conventional /usr/share/sbom path. It is a no-op-safe reflection of what the
// overlay contributed, not a full re-inventory of the baseline.
func generateOverlaySBOM(info *BaselineInfo, rootMount string, plan *ResolutionPlan) error {
	if plan == nil {
		return nil
	}
	pkgType := pkgTypeDeb
	if info != nil && info.PackageType != "" {
		pkgType = info.PackageType
	}

	pkgs := make([]ospackage.PackageInfo, 0, len(plan.ToInstall))
	for _, rp := range plan.ToInstall {
		pkgs = append(pkgs, ospackage.PackageInfo{
			Name:    rp.Name,
			PkgName: rp.Name,
			Type:    pkgType,
			Version: rp.Version,
			Arch:    rp.Arch,
			URL:     rp.URL,
		})
	}

	tempSBOM := filepath.Join(config.TempDir(), manifest.DefaultSPDXFile)
	if err := manifest.WriteSPDXToFile(pkgs, tempSBOM); err != nil {
		return fmt.Errorf("writing overlay SBOM: %w", err)
	}
	if err := manifest.CopySBOMToChroot(rootMount); err != nil {
		return fmt.Errorf("embedding overlay SBOM into baseline: %w", err)
	}
	log.Infof("Overlay SBOM generated for %d added package(s)", len(pkgs))
	return nil
}

// emitOverlayArtifact moves the modified baseline copy into the image build
// directory as "<name>-<version>.raw" and copies the SBOM sidecar alongside it,
// mirroring the create-mode RAW artifact naming. It returns the final image path.
//
// The loop device must already be detached (the backing file is moved, not the
// live device).
func emitOverlayArtifact(template *config.ImageTemplate, copyPath, version string) (string, error) {
	if strings.TrimSpace(copyPath) == "" {
		return "", fmt.Errorf("overlay emit: baseline copy path is empty")
	}
	buildDir, err := overlayImageBuildDir(template)
	if err != nil {
		return "", err
	}
	// Create the build directory in-process rather than via `bash -c "mkdir -p"`:
	// buildDir derives from user-controlled template fields, so a shell command
	// would be exposed to metacharacter/whitespace parsing. The workspace copy is
	// user-owned, so no sudo is needed here.
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", fmt.Errorf("overlay emit: failed to create image build directory %s: %w", buildDir, err)
	}

	finalPath := filepath.Join(buildDir, fmt.Sprintf("%s-%s.raw", template.GetImageName(), version))
	// Move the finished baseline into place without a shell (same rationale as the
	// mkdir above). os.Rename covers the common same-filesystem case; fall back to
	// a copy+remove when the workspace and build directory are on different mounts
	// (os.Rename returns EXDEV), which a cross-device `mv` would otherwise handle.
	if err := moveFile(copyPath, finalPath); err != nil {
		return "", fmt.Errorf("overlay emit: failed to move %s to %s: %w", copyPath, finalPath, err)
	}

	// Best-effort SBOM sidecar next to the artifact; absence is not fatal.
	if err := manifest.CopySBOMToImageBuildDir(buildDir); err != nil {
		log.Warnf("Overlay emit: failed to copy SBOM sidecar to %s: %v", buildDir, err)
	}
	return finalPath, nil
}

// moveFile moves src to dst without invoking a shell. It first attempts an atomic
// os.Rename; if that fails with a cross-device link error (src and dst live on
// different filesystems, which os.Rename cannot span), it falls back to a
// streaming copy followed by removing the source, mirroring what `mv` would do.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}

	// Cross-device: copy then remove the source.
	if err := copyLocalFile(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("failed to remove source %s after cross-device move: %w", src, err)
	}
	return nil
}

// overlayImageBuildDir returns the image build directory for the template, matching
// the create-mode layout (<workDir>/<providerId>/imagebuild/<systemConfigName>).
func overlayImageBuildDir(template *config.ImageTemplate) (string, error) {
	globalWorkDir, err := config.WorkDir()
	if err != nil {
		return "", fmt.Errorf("overlay emit: failed to resolve work directory: %w", err)
	}
	providerID := system.GetProviderId(template.Target.OS, template.Target.Dist, template.Target.Arch)
	return filepath.Join(globalWorkDir, providerID, "imagebuild", template.GetSystemConfigName()), nil
}
