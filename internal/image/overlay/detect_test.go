package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-edge-platform/image-composer-tool/internal/config"
)

// writeFile writes content to a path under dir, creating parent directories.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestParseOSReleaseFields(t *testing.T) {
	raw := `# a comment
NAME="Ubuntu"
ID=ubuntu
VERSION_ID="24.04"
ID_LIKE=debian
EMPTYLINE
`
	got := parseOSReleaseFields(raw)
	if got["ID"] != "ubuntu" {
		t.Errorf("ID = %q, want ubuntu", got["ID"])
	}
	if got["VERSION_ID"] != "24.04" {
		t.Errorf("VERSION_ID = %q, want 24.04", got["VERSION_ID"])
	}
	if got["NAME"] != "Ubuntu" {
		t.Errorf("NAME = %q, want Ubuntu", got["NAME"])
	}
}

func TestReadOSRelease_PrefersEtcThenUsrLib(t *testing.T) {
	t.Run("missing both fails", func(t *testing.T) {
		root := t.TempDir()
		if _, err := readOSRelease(root); err == nil {
			t.Fatal("expected error when no os-release present")
		}
	})

	t.Run("falls back to usr/lib", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "usr/lib/os-release", "ID=debian\nVERSION_ID=13\n")
		got, err := readOSRelease(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["ID"] != "debian" {
			t.Errorf("ID = %q, want debian", got["ID"])
		}
	})
}

func TestDetectPackageManager(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		wantMgr PackageManager
		wantTyp string
		wantErr bool
	}{
		{"dpkg", []string{"var/lib/dpkg/status"}, PackageManagerAPT, pkgTypeDeb, false},
		{"rpm bdb", []string{"var/lib/rpm/Packages"}, PackageManagerDNF, pkgTypeRPM, false},
		{"rpm sqlite", []string{"var/lib/rpm/rpmdb.sqlite"}, PackageManagerDNF, pkgTypeRPM, false},
		{"rpm ndb", []string{"var/lib/rpm/Packages.db"}, PackageManagerDNF, pkgTypeRPM, false},
		{"none", nil, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range tt.files {
				writeFile(t, root, f, "x")
			}
			mgr, typ, err := detectPackageManager(root)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mgr != tt.wantMgr || typ != tt.wantTyp {
				t.Errorf("got %s/%s, want %s/%s", mgr, typ, tt.wantMgr, tt.wantTyp)
			}
		})
	}
}

func TestDetectKernels(t *testing.T) {
	root := t.TempDir()
	// modules dirs (one non-version "build" dir must be ignored)
	for _, d := range []string{"lib/modules/6.8.0-31-generic", "lib/modules/6.8.0-40-generic", "lib/modules/build"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// a vmlinuz in /boot whose version is already covered, plus a fresh one
	writeFile(t, root, "boot/vmlinuz-6.8.0-31-generic", "x")
	writeFile(t, root, "boot/vmlinuz-6.9.0-1-generic", "x")
	writeFile(t, root, "boot/config-ignored", "x")

	got := detectKernels(root)
	want := []string{"6.8.0-31-generic", "6.8.0-40-generic", "6.9.0-1-generic"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("kernels = %v, want %v", got, want)
	}
}

func TestDetectKernels_NoneFound(t *testing.T) {
	if got := detectKernels(t.TempDir()); got != nil {
		t.Errorf("expected nil for empty root, got %v", got)
	}
}

func TestDetectBootloader(t *testing.T) {
	tests := []struct {
		name string
		dirs []string
		want string
	}{
		{"grub2", []string{"boot/grub2"}, "grub2"},
		{"grub", []string{"boot/grub"}, "grub2"},
		{"systemd-boot loader", []string{"boot/efi/loader"}, "systemd-boot"},
		{"uki", []string{"boot/efi/EFI/Linux"}, "uki"},
		{"unknown", nil, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range tt.dirs {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if got := detectBootloader(root); got != tt.want {
				t.Errorf("bootloader = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeArch(t *testing.T) {
	tests := map[string]string{
		"x86_64": "x86_64", "amd64": "x86_64",
		"aarch64": "aarch64", "arm64": "aarch64",
		"i686": "x86", "riscv64": "riscv64",
	}
	for in, want := range tests {
		if got := normalizeArch(in); got != want {
			t.Errorf("normalizeArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLeadingDigits(t *testing.T) {
	tests := map[string]string{
		"ubuntu24": "24", "24.04": "24", "azl3": "3",
		"debian13": "13", "noversion": "", "3.0": "3",
	}
	for in, want := range tests {
		if got := leadingDigits(in); got != want {
			t.Errorf("leadingDigits(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateAgainstTarget(t *testing.T) {
	base := &BaselineInfo{OS: "ubuntu", DistroID: "ubuntu", Version: "24.04", Arch: "x86_64"}

	tests := []struct {
		name    string
		info    BaselineInfo
		target  config.TargetInfo
		wantErr string // substring; "" means no error
	}{
		{
			name:   "match",
			info:   *base,
			target: config.TargetInfo{OS: "ubuntu", Dist: "ubuntu24", Arch: "x86_64"},
		},
		{
			name:   "arch alias matches amd64",
			info:   *base,
			target: config.TargetInfo{OS: "ubuntu", Dist: "ubuntu24", Arch: "amd64"},
		},
		{
			name:    "os mismatch",
			info:    *base,
			target:  config.TargetInfo{OS: "debian", Dist: "debian13", Arch: "x86_64"},
			wantErr: "OS mismatch",
		},
		{
			name:    "arch mismatch",
			info:    *base,
			target:  config.TargetInfo{OS: "ubuntu", Dist: "ubuntu24", Arch: "aarch64"},
			wantErr: "architecture mismatch",
		},
		{
			name:    "version mismatch",
			info:    *base,
			target:  config.TargetInfo{OS: "ubuntu", Dist: "ubuntu22", Arch: "x86_64"},
			wantErr: "version mismatch",
		},
		{
			name:   "azl version match",
			info:   BaselineInfo{OS: "azure-linux", DistroID: "azurelinux", Version: "3.0", Arch: "x86_64"},
			target: config.TargetInfo{OS: "azure-linux", Dist: "azl3", Arch: "x86_64"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAgainstTarget(&tt.info, tt.target)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseDpkgStatus(t *testing.T) {
	root := t.TempDir()
	status := `Package: bash
Status: install ok installed
Architecture: amd64
Version: 5.2.21-2ubuntu4
Depends: libc6 (>= 2.34), libtinfo6 (>= 6)
Pre-Depends: dpkg (>= 1.17.14)
Provides: ash

Package: ghost-config
Status: deinstall ok config-files
Architecture: all
Version: 1.0

Package: vim
Status: install ok installed
Architecture: amd64
Version: 2:9.1.0016-1ubuntu7
Depends: vim-common (= 2:9.1.0016-1ubuntu7) | vim-gui-common, libc6 (>= 2.34)
`
	writeFile(t, root, "var/lib/dpkg/status", status)

	pkgs, err := parseDpkgStatus(filepath.Join(root, "var", "lib", "dpkg", "status"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pkgs) != 3 {
		t.Fatalf("got %d packages, want 3", len(pkgs))
	}

	byName := map[string]BaselinePackage{}
	for _, p := range pkgs {
		byName[p.Name] = p
	}

	bash := byName["bash"]
	if !bash.Installed {
		t.Error("bash should be Installed")
	}
	if bash.Version != "5.2.21-2ubuntu4" || bash.Arch != "amd64" {
		t.Errorf("bash version/arch = %s/%s", bash.Version, bash.Arch)
	}
	wantDeps := "dpkg,libc6,libtinfo6"
	if strings.Join(bash.Dependencies, ",") != wantDeps {
		t.Errorf("bash deps = %v, want %s", bash.Dependencies, wantDeps)
	}
	if strings.Join(bash.Provides, ",") != "ash" {
		t.Errorf("bash provides = %v, want [ash]", bash.Provides)
	}

	if byName["ghost-config"].Installed {
		t.Error("config-files package must not be Installed")
	}

	// Alternatives reduce to the first; version constraints stripped.
	vim := byName["vim"]
	wantVimDeps := "vim-common,libc6"
	if strings.Join(vim.Dependencies, ",") != wantVimDeps {
		t.Errorf("vim deps = %v, want %s", vim.Dependencies, wantVimDeps)
	}
}

func TestParseDpkgStatus_Errors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := parseDpkgStatus(filepath.Join(t.TempDir(), "status")); err == nil {
			t.Fatal("expected error for missing status file")
		}
	})
	t.Run("empty database", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "status", "\n\n")
		if _, err := parseDpkgStatus(filepath.Join(root, "status")); err == nil {
			t.Fatal("expected error for empty database")
		}
	})
}

func TestDebPackageName(t *testing.T) {
	tests := map[string]string{
		"libc6 (>= 2.34)":       "libc6",
		"python3:any":           "python3",
		"  bash  ":              "bash",
		"libfoo:amd64 (>= 1.0)": "libfoo",
	}
	for in, want := range tests {
		if got := debPackageName(in); got != want {
			t.Errorf("debPackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsDpkgInstalled(t *testing.T) {
	tests := map[string]bool{
		"install ok installed":      true,
		"deinstall ok config-files": false,
		"install ok half-installed": false,
		"":                          false,
	}
	for in, want := range tests {
		if got := isDpkgInstalled(in); got != want {
			t.Errorf("isDpkgInstalled(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFilterRPMRequires(t *testing.T) {
	in := []string{
		"rpmlib(CompressedFileNames)",
		"glibc",
		"config(mypkg)",
		"/bin/sh",
		"libc.so.6()(64bit)",
		"glibc", // duplicate
		"",
	}
	got := filterRPMRequires(in)
	want := "glibc,libc.so.6()(64bit)"
	if strings.Join(got, ",") != want {
		t.Errorf("filterRPMRequires = %v, want %s", got, want)
	}
}

func TestDedupeStrings(t *testing.T) {
	got := dedupeStrings([]string{"a", "b", "a", "", "c", "b"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("dedupeStrings = %v, want [a b c]", got)
	}
	if dedupeStrings(nil) != nil {
		t.Error("dedupeStrings(nil) should be nil")
	}
}

// TestDetectBaseline_DebDistros covers Ubuntu and Debian end-to-end against a
// fake mounted root (os-release + dpkg DB + ELF probe binary), exercising
// DetectBaseline's full success path without root or real mounts.
func TestDetectBaseline_DebDistros(t *testing.T) {
	tests := []struct {
		name      string
		distroID  string
		versionID string
		wantOS    string
		target    config.TargetInfo
	}{
		{"ubuntu", "ubuntu", "24.04", "ubuntu", config.TargetInfo{OS: "ubuntu", Dist: "ubuntu24", Arch: "x86_64"}},
		{"debian", "debian", "13", "debian", config.TargetInfo{OS: "debian", Dist: "debian13", Arch: "x86_64"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newFakeRoot(t, tt.distroID, tt.versionID)
			writeFile(t, root, "var/lib/dpkg/status",
				"Package: bash\nStatus: install ok installed\nArchitecture: amd64\nVersion: 5.2-1\nDepends: libc6\n")

			// The fake ELF probe binary is the test binary itself, so detection
			// reports the host arch; align the target so the match check passes.
			tt.target.Arch = hostArch(t)

			info, pkgs, err := DetectBaseline(root, tt.target)
			if err != nil {
				t.Fatalf("DetectBaseline: %v", err)
			}
			if info.OS != tt.wantOS || info.DistroID != tt.distroID {
				t.Errorf("OS=%s ID=%s, want %s/%s", info.OS, info.DistroID, tt.wantOS, tt.distroID)
			}
			if info.Version != tt.versionID {
				t.Errorf("version = %q, want %q", info.Version, tt.versionID)
			}
			if info.PackageManager != PackageManagerAPT || info.PackageType != pkgTypeDeb {
				t.Errorf("pkgmgr = %s/%s, want apt/deb", info.PackageManager, info.PackageType)
			}
			if normalizeArch(info.Arch) != normalizeArch(tt.target.Arch) {
				t.Errorf("arch = %q, want %q", info.Arch, tt.target.Arch)
			}
			if len(pkgs) != 1 || pkgs[0].Name != "bash" {
				t.Errorf("packages = %+v, want one bash", pkgs)
			}
		})
	}
}

// TestDetectBaseline_RPMDistros covers AZL and EMT detection up to the package
// inventory. The inventory itself shells out to rpm, so these stop at the
// pre-inventory checks by asserting the failure originates from the rpm read.
func TestDetectBaseline_RPMDistros(t *testing.T) {
	tests := []struct {
		name      string
		distroID  string
		versionID string
		wantOS    string
		target    config.TargetInfo
	}{
		{"azl", "azurelinux", "3.0", "azure-linux", config.TargetInfo{OS: "azure-linux", Dist: "azl3", Arch: "x86_64"}},
		{"emt", "emt", "3.0", "edge-microvisor-toolkit", config.TargetInfo{OS: "edge-microvisor-toolkit", Dist: "emt3", Arch: "x86_64"}},
		{"mariner alias", "mariner", "2.0", "azure-linux", config.TargetInfo{OS: "azure-linux", Dist: "azl2", Arch: "x86_64"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newFakeRoot(t, tt.distroID, tt.versionID)
			writeFile(t, root, "var/lib/rpm/Packages", "fake-bdb")
			tt.target.Arch = hostArch(t)

			// The OS/arch/pkg-manager/target checks all pass; only the rpm
			// inventory read can fail here (no real rpm DB), so any error must
			// come from that stage and the detection logic above is validated.
			info, _, err := DetectBaseline(root, tt.target)
			if err != nil {
				if !strings.Contains(err.Error(), "rpm") {
					t.Fatalf("unexpected pre-inventory failure: %v", err)
				}
				return
			}
			if info.OS != tt.wantOS {
				t.Errorf("OS = %s, want %s", info.OS, tt.wantOS)
			}
			if info.PackageManager != PackageManagerDNF {
				t.Errorf("pkgmgr = %s, want dnf", info.PackageManager)
			}
		})
	}
}

func TestDetectBaseline_Errors(t *testing.T) {
	target := config.TargetInfo{OS: "ubuntu", Dist: "ubuntu24", Arch: "x86_64"}

	t.Run("no os-release", func(t *testing.T) {
		root := t.TempDir()
		if _, _, err := DetectBaseline(root, target); err == nil {
			t.Fatal("expected error for missing os-release")
		}
	})

	t.Run("unsupported distro ID", func(t *testing.T) {
		root := newFakeRoot(t, "gentoo", "2.0")
		writeFile(t, root, "var/lib/dpkg/status", "Package: x\nStatus: install ok installed\n")
		_, _, err := DetectBaseline(root, target)
		if err == nil || !strings.Contains(err.Error(), "identify baseline OS") {
			t.Fatalf("error = %v, want unsupported-distro error", err)
		}
	})

	t.Run("os mismatch with target", func(t *testing.T) {
		root := newFakeRoot(t, "debian", "13")
		writeFile(t, root, "var/lib/dpkg/status", "Package: x\nStatus: install ok installed\n")
		_, _, err := DetectBaseline(root, target) // target is ubuntu
		if err == nil || !strings.Contains(err.Error(), "OS mismatch") {
			t.Fatalf("error = %v, want OS mismatch", err)
		}
	})

	t.Run("no package database", func(t *testing.T) {
		root := newFakeRoot(t, "ubuntu", "24.04") // ELF + os-release, but no pkg DB
		_, _, err := DetectBaseline(root, target)
		if err == nil || !strings.Contains(err.Error(), "package manager") {
			t.Fatalf("error = %v, want package-manager error", err)
		}
	})

	t.Run("no arch probe binary", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "etc/os-release", "ID=ubuntu\nVERSION_ID=24.04\n")
		writeFile(t, root, "var/lib/dpkg/status", "Package: x\nStatus: install ok installed\n")
		_, _, err := DetectBaseline(root, target)
		if err == nil || !strings.Contains(err.Error(), "architecture") {
			t.Fatalf("error = %v, want architecture error", err)
		}
	})
}

// newFakeRoot builds a temp directory that looks enough like a mounted Linux
// root for detection: an os-release file and a real x86_64 ELF binary at one of
// the architecture probe paths. It does NOT create a package database.
func newFakeRoot(t *testing.T, distroID, versionID string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "etc/os-release",
		"ID="+distroID+"\nVERSION_ID=\""+versionID+"\"\nNAME=test\n")

	// Copy the test binary itself (a real ELF on the build host) to a probe path.
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read self: %v", err)
	}
	writeFile(t, root, "usr/lib/systemd/systemd", string(data))
	return root
}

// hostArch returns the arch detected from the test binary itself, so target
// assertions stay correct regardless of which architecture the suite runs on.
func hostArch(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	arch, err := elfArch(self)
	if err != nil {
		t.Fatalf("elfArch(self): %v", err)
	}
	return arch
}
