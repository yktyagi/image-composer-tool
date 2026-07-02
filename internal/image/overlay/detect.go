package overlay

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/open-edge-platform/image-composer-tool/internal/config"
	"github.com/open-edge-platform/image-composer-tool/internal/utils/shell"
)

// PackageManager identifies the package-manager family of a baseline image.
type PackageManager string

const (
	// PackageManagerAPT is the dpkg/apt family (Debian, Ubuntu, eLxr).
	PackageManagerAPT PackageManager = "apt"
	// PackageManagerDNF is the rpm/dnf/tdnf family (Azure Linux, EMT).
	PackageManagerDNF PackageManager = "dnf"
)

// Package types, matching the values the rest of the tool uses (see chrootEnv
// GetTargetOsPkgType: "deb"/"rpm").
const (
	pkgTypeDeb = "deb"
	pkgTypeRPM = "rpm"
)

// distroIDToOS maps an os-release ID (and legacy aliases) to the internal OS
// name used by config.TargetInfo.OS and the provider registry (OsName).
var distroIDToOS = map[string]string{
	"ubuntu":     "ubuntu",
	"debian":     "debian",
	"azurelinux": "azure-linux",
	"mariner":    "azure-linux", // legacy Azure Linux / CBL-Mariner ID
	"emt":        "edge-microvisor-toolkit",
	"elxr":       "wind-river-elxr",
}

// osReleaseCandidates are the standard os-release locations, in priority order.
var osReleaseCandidates = []string{
	filepath.Join("etc", "os-release"),
	filepath.Join("usr", "lib", "os-release"),
}

// archProbeBinaries are ELF binaries commonly present in a Linux root, probed in
// order to determine the baseline architecture. They are deliberately a mix of
// deb- and rpm-distro binaries so at least one resolves on any supported layout.
var archProbeBinaries = []string{
	filepath.Join("usr", "lib", "systemd", "systemd"),
	filepath.Join("bin", "bash"),
	filepath.Join("usr", "bin", "bash"),
	filepath.Join("usr", "bin", "dpkg"),
	filepath.Join("usr", "bin", "rpm"),
	filepath.Join("bin", "sh"),
	filepath.Join("usr", "bin", "sh"),
	filepath.Join("sbin", "init"),
	filepath.Join("bin", "cat"),
	filepath.Join("usr", "bin", "cat"),
}

// BaselinePackage is a normalized record for one package found in the baseline
// image's package database. It is the unit downstream policy evaluation consumes.
type BaselinePackage struct {
	// Name is the package name (e.g. "libc6", "bash").
	Name string
	// Version is the full version string, including release/epoch where present.
	Version string
	// Arch is the package architecture (e.g. "amd64", "x86_64", "all", "noarch").
	Arch string
	// Installed reports whether the package is fully installed (vs. config-files
	// only, half-installed, etc.). Only installed packages gate overlay policy.
	Installed bool
	// Dependencies are the canonical names of packages this package depends on,
	// with version constraints and alternatives reduced to package names.
	Dependencies []string
	// Provides are the virtual capabilities/names this package provides.
	Provides []string
}

// BaselineInfo is the structured description of a baseline image's OS, detected
// from its mounted root filesystem and handed to downstream overlay stages.
type BaselineInfo struct {
	// OS is the normalized internal OS name (matches config.TargetInfo.OS).
	OS string
	// DistroID is the raw os-release ID (e.g. "ubuntu", "azurelinux").
	DistroID string
	// Version is the os-release VERSION_ID (e.g. "24.04", "3.0").
	Version string
	// Arch is the normalized architecture (e.g. "x86_64", "aarch64").
	Arch string
	// PackageManager is the detected package-manager family.
	PackageManager PackageManager
	// PackageType is the package artifact type, "deb" or "rpm".
	PackageType string
	// Kernels are the installed kernel versions, sorted and de-duplicated.
	Kernels []string
	// Bootloader is the detected bootloader type ("grub2", "systemd-boot",
	// "uki", or "unknown").
	Bootloader string
}

// DetectBaseline inspects the mounted baseline root at rootMount and returns its
// structured BaselineInfo plus the normalized installed-package inventory.
//
// It hard-fails when the OS, architecture, package manager, or package database
// cannot be identified/read, and when the detected OS, distro version, or
// architecture does not match the build target. The package inventory is only
// extracted after the cheaper target checks pass, so a mismatch fails fast.
func DetectBaseline(rootMount string, target config.TargetInfo) (*BaselineInfo, []BaselinePackage, error) {
	osRelease, err := readOSRelease(rootMount)
	if err != nil {
		return nil, nil, err
	}

	distroID := strings.ToLower(strings.TrimSpace(osRelease["ID"]))
	osName, ok := distroIDToOS[distroID]
	if !ok {
		return nil, nil, fmt.Errorf("unable to identify baseline OS: os-release ID %q is not a supported distribution", distroID)
	}

	arch, err := detectArch(rootMount)
	if err != nil {
		return nil, nil, err
	}

	pkgManager, pkgType, err := detectPackageManager(rootMount)
	if err != nil {
		return nil, nil, err
	}

	info := &BaselineInfo{
		OS:             osName,
		DistroID:       distroID,
		Version:        strings.TrimSpace(osRelease["VERSION_ID"]),
		Arch:           arch,
		PackageManager: pkgManager,
		PackageType:    pkgType,
		Kernels:        detectKernels(rootMount),
		Bootloader:     detectBootloader(rootMount),
	}

	// Validate against the build target before the expensive inventory read so
	// a mismatched baseline fails fast.
	if err := validateAgainstTarget(info, target); err != nil {
		return nil, nil, err
	}

	pkgs, err := extractPackages(rootMount, pkgType)
	if err != nil {
		return nil, nil, err
	}

	log.Infof("Detected baseline: OS=%s (ID=%s) version=%s arch=%s pkgmgr=%s kernels=%v bootloader=%s packages=%d",
		info.OS, info.DistroID, info.Version, info.Arch, info.PackageManager, info.Kernels, info.Bootloader, len(pkgs))

	return info, pkgs, nil
}

// readOSRelease reads and parses os-release from the mounted root. It fails if
// no os-release file is present or readable, since the OS cannot otherwise be
// identified.
func readOSRelease(rootMount string) (map[string]string, error) {
	var lastErr error
	for _, rel := range osReleaseCandidates {
		path := filepath.Join(rootMount, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			continue
		}
		return parseOSReleaseFields(string(data)), nil
	}
	return nil, fmt.Errorf("unable to read baseline os-release (tried %v under %s): %w",
		osReleaseCandidates, rootMount, lastErr)
}

// parseOSReleaseFields parses os-release style KEY=VALUE lines, stripping quotes.
func parseOSReleaseFields(raw string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			fields[key] = value
		}
	}
	return fields
}

// detectArch determines the baseline architecture by reading the ELF machine
// type of the first probe binary that resolves inside the root. Reading the ELF
// header directly is more reliable than trusting os-release (which omits arch).
func detectArch(rootMount string) (string, error) {
	for _, bin := range archProbeBinaries {
		path, err := resolveInRoot(rootMount, string(filepath.Separator)+bin)
		if err != nil {
			continue
		}
		arch, err := elfArch(path)
		if err != nil {
			continue
		}
		return arch, nil
	}
	return "", fmt.Errorf("unable to identify baseline architecture: no readable ELF binary found under %s (tried %v)",
		rootMount, archProbeBinaries)
}

// elfArch opens an ELF file and maps its machine type to a normalized arch.
func elfArch(path string) (string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	switch f.Machine {
	case elf.EM_X86_64:
		return "x86_64", nil
	case elf.EM_AARCH64:
		return "aarch64", nil
	case elf.EM_386:
		return "x86", nil
	case elf.EM_ARM:
		return "arm", nil
	case elf.EM_RISCV:
		return "riscv64", nil
	default:
		return "", fmt.Errorf("unsupported ELF machine type %v", f.Machine)
	}
}

// resolveInRoot resolves a possibly-symlinked path that is expressed relative to
// the baseline root, keeping all resolution confined to rootMount. It returns the
// host path of the final regular file, or an error if it does not resolve to one.
func resolveInRoot(rootMount, rootPath string) (string, error) {
	current := rootPath // absolute, relative to the baseline root
	for i := 0; i < 16; i++ {
		hostPath := filepath.Join(rootMount, current)
		fi, err := os.Lstat(hostPath)
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			if fi.Mode().IsRegular() {
				return hostPath, nil
			}
			return "", fmt.Errorf("%s is not a regular file", current)
		}
		target, err := os.Readlink(hostPath)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			current = target
		} else {
			current = filepath.Join(filepath.Dir(current), target)
		}
	}
	return "", fmt.Errorf("too many symlink levels resolving %s", rootPath)
}

// detectPackageManager identifies the package-manager family from the package
// database present in the root. It fails if neither a dpkg nor an rpm database
// is found, since the family then cannot be identified.
func detectPackageManager(rootMount string) (PackageManager, string, error) {
	if _, err := os.Stat(filepath.Join(rootMount, "var", "lib", "dpkg", "status")); err == nil {
		return PackageManagerAPT, pkgTypeDeb, nil
	}
	// rpm databases live under /var/lib/rpm in several on-disk formats
	// (BerkeleyDB "Packages", ndb "Packages.db", or sqlite "rpmdb.sqlite").
	for _, db := range []string{"Packages", "Packages.db", "rpmdb.sqlite"} {
		if _, err := os.Stat(filepath.Join(rootMount, "var", "lib", "rpm", db)); err == nil {
			return PackageManagerDNF, pkgTypeRPM, nil
		}
	}
	return "", "", fmt.Errorf("unable to identify baseline package manager: no dpkg (var/lib/dpkg/status) or rpm (var/lib/rpm) database found under %s", rootMount)
}

// detectKernels returns the installed kernel versions, gathered from the kernel
// module directories and any vmlinuz images in /boot. Detection is best-effort:
// an image with no discoverable kernel yields an empty slice and a warning.
func detectKernels(rootMount string) []string {
	seen := map[string]bool{}

	if entries, err := os.ReadDir(filepath.Join(rootMount, "lib", "modules")); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.Contains(e.Name(), ".") {
				seen[e.Name()] = true
			}
		}
	}

	if entries, err := os.ReadDir(filepath.Join(rootMount, "boot")); err == nil {
		for _, e := range entries {
			if v := strings.TrimPrefix(e.Name(), "vmlinuz-"); v != e.Name() && v != "" {
				seen[v] = true
			}
		}
	}

	if len(seen) == 0 {
		log.Warnf("No installed kernel found under %s (lib/modules or boot/vmlinuz-*)", rootMount)
		return nil
	}

	kernels := make([]string, 0, len(seen))
	for k := range seen {
		kernels = append(kernels, k)
	}
	sort.Strings(kernels)
	return kernels
}

// detectBootloader classifies the bootloader type by inspecting well-known paths
// under the mounted root (the ESP is mounted at <root>/boot/efi). It is a
// best-effort classification used for reporting, not a gate.
func detectBootloader(rootMount string) string {
	exists := func(parts ...string) bool {
		_, err := os.Stat(filepath.Join(append([]string{rootMount}, parts...)...))
		return err == nil
	}

	switch {
	case exists("boot", "grub2"), exists("boot", "grub"):
		return "grub2"
	case exists("boot", "efi", "EFI", "systemd"), exists("boot", "efi", "loader"), exists("boot", "loader", "entries"):
		return "systemd-boot"
	case exists("boot", "efi", "EFI", "Linux"):
		return "uki"
	default:
		return "unknown"
	}
}

// validateAgainstTarget hard-fails when the detected baseline OS, distro version,
// or architecture does not match the build target.
func validateAgainstTarget(info *BaselineInfo, target config.TargetInfo) error {
	if !strings.EqualFold(info.OS, strings.TrimSpace(target.OS)) {
		return fmt.Errorf("baseline OS mismatch: detected %q (os-release ID %q) but target requires %q",
			info.OS, info.DistroID, target.OS)
	}

	if normalizeArch(info.Arch) != normalizeArch(target.Arch) {
		return fmt.Errorf("baseline architecture mismatch: detected %q but target requires %q",
			info.Arch, target.Arch)
	}

	// The target dist (e.g. "ubuntu24", "azl3", "debian13") embeds a major
	// version; compare it to the detected VERSION_ID major when both are present.
	wantMajor := leadingDigits(target.Dist)
	gotMajor := leadingDigits(info.Version)
	if wantMajor != "" && gotMajor != "" && wantMajor != gotMajor {
		return fmt.Errorf("baseline distro version mismatch: detected version %q (major %q) but target dist %q expects major %q",
			info.Version, gotMajor, target.Dist, wantMajor)
	}

	return nil
}

// normalizeArch maps architecture aliases to a canonical form so equivalent
// names (x86_64/amd64, aarch64/arm64) compare equal.
func normalizeArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "x86_64", "amd64":
		return "x86_64"
	case "aarch64", "arm64":
		return "aarch64"
	case "i386", "i686", "x86":
		return "x86"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

// leadingDigits returns the first maximal run of digits in s, or "" if none.
// "ubuntu24" -> "24", "24.04" -> "24", "azl3" -> "3".
func leadingDigits(s string) string {
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			if start == -1 {
				start = i
			}
		} else if start != -1 {
			return s[start:i]
		}
	}
	if start != -1 {
		return s[start:]
	}
	return ""
}

// extractPackages reads the baseline's package inventory using the distro-native
// database and normalizes it into BaselinePackage records.
func extractPackages(rootMount, pkgType string) ([]BaselinePackage, error) {
	switch pkgType {
	case pkgTypeDeb:
		return parseDpkgStatus(filepath.Join(rootMount, "var", "lib", "dpkg", "status"))
	case pkgTypeRPM:
		return queryRPMPackages(rootMount)
	default:
		return nil, fmt.Errorf("unsupported package type %q for inventory extraction", pkgType)
	}
}

// parseDpkgStatus parses a dpkg status file (deb822 stanzas) into normalized
// package records. Only stanzas whose Status marks the package as installed are
// flagged Installed=true; the full set is returned so callers see config-files
// remnants too.
func parseDpkgStatus(statusPath string) ([]BaselinePackage, error) {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read dpkg status database %s: %w", statusPath, err)
	}

	var pkgs []BaselinePackage
	for _, stanza := range strings.Split(string(data), "\n\n") {
		if strings.TrimSpace(stanza) == "" {
			continue
		}
		fields := parseDeb822Fields(stanza)
		name := fields["Package"]
		if name == "" {
			continue
		}

		var deps []string
		deps = append(deps, parseDebDependencies(fields["Pre-Depends"])...)
		deps = append(deps, parseDebDependencies(fields["Depends"])...)

		pkgs = append(pkgs, BaselinePackage{
			Name:         name,
			Version:      fields["Version"],
			Arch:         fields["Architecture"],
			Installed:    isDpkgInstalled(fields["Status"]),
			Dependencies: dedupeStrings(deps),
			Provides:     parseDebProvides(fields["Provides"]),
		})
	}

	if len(pkgs) == 0 {
		return nil, fmt.Errorf("dpkg status database %s contained no packages", statusPath)
	}
	return pkgs, nil
}

// parseDeb822Fields parses a single deb822 stanza into its top-level fields,
// folding RFC822 continuation lines (those beginning with whitespace) into the
// preceding field value.
func parseDeb822Fields(stanza string) map[string]string {
	fields := map[string]string{}
	lastKey := ""
	for _, line := range strings.Split(stanza, "\n") {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if lastKey != "" {
				fields[lastKey] += " " + strings.TrimSpace(line)
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		fields[key] = strings.TrimSpace(value)
		lastKey = key
	}
	return fields
}

// isDpkgInstalled reports whether a dpkg Status line ("want flag state") marks a
// fully installed package, i.e. the state field is "installed".
func isDpkgInstalled(status string) bool {
	fields := strings.Fields(status)
	if len(fields) == 0 {
		return false
	}
	return fields[len(fields)-1] == "installed"
}

// parseDebDependencies reduces a Depends/Pre-Depends field to canonical package
// names: comma-separated relations, each with "|" alternatives reduced to the
// first alternative, version constraints "(...)" and arch/multiarch qualifiers
// stripped.
func parseDebDependencies(field string) []string {
	if strings.TrimSpace(field) == "" {
		return nil
	}
	var names []string
	for _, relation := range strings.Split(field, ",") {
		alternatives := strings.Split(relation, "|")
		if len(alternatives) == 0 {
			continue
		}
		if name := debPackageName(alternatives[0]); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// parseDebProvides reduces a Provides field to the provided package names.
func parseDebProvides(field string) []string {
	if strings.TrimSpace(field) == "" {
		return nil
	}
	var names []string
	for _, item := range strings.Split(field, ",") {
		if name := debPackageName(item); name != "" {
			names = append(names, name)
		}
	}
	return dedupeStrings(names)
}

// debPackageName extracts the bare package name from a single dependency atom,
// dropping any version constraint and ":arch"/":any" multiarch qualifier.
func debPackageName(atom string) string {
	atom = strings.TrimSpace(atom)
	if idx := strings.IndexByte(atom, '('); idx != -1 {
		atom = strings.TrimSpace(atom[:idx])
	}
	if idx := strings.IndexByte(atom, ':'); idx != -1 {
		atom = strings.TrimSpace(atom[:idx])
	}
	return atom
}

// queryRPMPackages reads the rpm database under rootMount via the host rpm with
// --root, normalizing one record per package. A single fielded query keeps it to
// one rpm invocation; the dependency column lists REQUIRENAME entries.
func queryRPMPackages(rootMount string) ([]BaselinePackage, error) {
	// Fields are pipe-separated; the trailing [%{REQUIRENAME} ] expands the
	// requires array space-separated within the last column.
	const qf = `%{NAME}|%{EVR}|%{ARCH}|[%{REQUIRENAME} ]\n`
	// rootMount is a workspace-derived path interpolated into a bash -c command;
	// single-quote it with shell.QuoteArg so it cannot alter shell parsing.
	cmd := fmt.Sprintf("rpm --root %s -qa --qf '%s'", shell.QuoteArg(rootMount), qf)
	out, err := shell.ExecCmd(cmd, true, shell.HostPath, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to read rpm database under %s: %w", rootMount, err)
	}

	var pkgs []BaselinePackage
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 3 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		var deps []string
		if len(parts) == 4 {
			deps = filterRPMRequires(strings.Fields(parts[3]))
		}
		pkgs = append(pkgs, BaselinePackage{
			Name:    strings.TrimSpace(parts[0]),
			Version: strings.TrimSpace(parts[1]),
			Arch:    strings.TrimSpace(parts[2]),
			// rpm -qa lists only installed packages.
			Installed:    true,
			Dependencies: deps,
		})
	}

	if len(pkgs) == 0 {
		return nil, fmt.Errorf("rpm database under %s contained no packages", rootMount)
	}
	return pkgs, nil
}

// filterRPMRequires drops synthetic rpm requires (rpmlib/config/file-path
// dependencies) and de-duplicates, leaving package-name-like capabilities.
func filterRPMRequires(requires []string) []string {
	var out []string
	for _, r := range requires {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if strings.HasPrefix(r, "rpmlib(") || strings.HasPrefix(r, "config(") ||
			strings.HasPrefix(r, "/") {
			continue
		}
		out = append(out, r)
	}
	return dedupeStrings(out)
}

// dedupeStrings returns s with duplicates removed, preserving first-seen order.
func dedupeStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(s))
	var out []string
	for _, v := range s {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
