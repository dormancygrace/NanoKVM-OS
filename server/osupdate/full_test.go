package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fullFixture() Manifest {
	digest := strings.Repeat("a", 64)
	return Manifest{Format: 4, Kind: "full-system", Product: "NanoKVM OS", Arch: "riscv64", Version: "1.0.0-beta.7", Sequence: 20, PayloadBytes: 1000, PayloadSHA256: digest,
		Full:  &FullSystemUpdate{Platform: FullPlatform, MinimumUpdater: UpdaterCapability, TargetUpdater: UpdaterCapability, TargetUpdaterSHA256: digest, AddonContractSHA256: digest, RootFSBytes: FullRootFSBytes, RootFSSHA256: digest, BootloaderSHA256: digest, KernelRelease: "7.2.5-nanokvm-os-beta7"},
		Files: []Entry{{Path: "normal-boot.sd", Size: 100, Mode: 0644, SHA256: digest}, {Path: "ram-update.sd", Size: 100, Mode: 0644, SHA256: digest}, {Path: "rootfs.ext4.gz", Size: 100 << 20, Mode: 0644, SHA256: digest}, {Path: "addons-contract.json", Size: 100, Mode: 0644, SHA256: digest}}}
}
func TestFullManifestLegacyUpdaterMetadataIsRecognized(t *testing.T) {
	m := fullFixture()
	m.Full.TargetUpdater = 0
	m.Full.TargetUpdaterSHA256 = ""
	m.Full.MinimumUpdater = 1
	m.Full.AddonContractSHA256 = ""
	m.Files = m.Files[:3]
	if err := validateManifest(m); err != nil {
		t.Fatal(err)
	}
	if err := (&fullUpdater{root: t.TempDir()}).check(m); err == nil || !strings.Contains(err.Error(), "older updater") {
		t.Fatalf("legacy package could regress independent helper: %v", err)
	}
}
func TestLegacyFullPackageBlocksInstalledAddons(t *testing.T) {
	m := fullFixture()
	m.Full.TargetUpdater = 0
	m.Full.TargetUpdaterSHA256 = ""
	m.Full.MinimumUpdater = 1
	m.Full.AddonContractSHA256 = ""
	m.Files = m.Files[:3]
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "kvmapp/.os-update"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, "kvmapp/.os-update/updater.json"), UpdaterState{Capability: 1}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "opt/nkos/etc/apk"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "opt/nkos/etc/apk/world"), []byte("nkos-addon-demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&fullUpdater{root: root}).check(m); err == nil || !strings.Contains(err.Error(), "cannot preserve installed addons") {
		t.Fatalf("legacy package did not block addon loss: %v", err)
	}
}
func TestFullManifestDoesNotBindSourceABI(t *testing.T) {
	m := fullFixture()
	if err := validateManifest(m); err != nil {
		t.Fatal(err)
	}
	// Entire rootfs replaces source dependencies: no source version, fingerprint or native ABI is required.
	if m.SystemBase != "" || m.NativeABI != "" {
		t.Fatal("source dependency binding")
	}
}
func TestFullManifestRejectsInvalidImages(t *testing.T) {
	cases := map[string]func(*Manifest){
		"traversal":     func(m *Manifest) { m.Files[0].Path = "../boot.sd" },
		"extra":         func(m *Manifest) { m.Files = append(m.Files, Entry{Path: "evil"}) },
		"duplicate":     func(m *Manifest) { m.Files[0] = m.Files[1] },
		"symlink":       func(m *Manifest) { m.Files[0].Link = "/boot/boot.sd" },
		"preserve":      func(m *Manifest) { m.Files[0].Preserve = true },
		"mode":          func(m *Manifest) { m.Files[0].Mode = 0755 },
		"hash":          func(m *Manifest) { m.Files[0].SHA256 = "bad" },
		"layout":        func(m *Manifest) { m.Full.RootFSBytes++ },
		"platform":      func(m *Manifest) { m.Full.Platform = "other" },
		"metadata":      func(m *Manifest) { m.Full = nil },
		"addon digest":  func(m *Manifest) { m.Full.AddonContractSHA256 = strings.Repeat("b", 64) },
		"missing addon": func(m *Manifest) { m.Files = m.Files[:3] },
		"legacy kernel": func(m *Manifest) { m.Kernel = &KernelUpdate{} },
		"source base":   func(m *Manifest) { m.SystemBase = strings.Repeat("b", 64) },
		"remove":        func(m *Manifest) { m.Remove = []string{"rootfs/usr/bin/foo"} },
		"oversize FIT":  func(m *Manifest) { m.Files[0].Size = 17 << 20 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := fullFixture()
			change(&m)
			if validateManifest(m) == nil {
				t.Fatal("accepted invalid full-system package")
			}
		})
	}
}
func TestFullUpdaterUpgradeBoundary(t *testing.T) {
	m := fullFixture()
	m.Full.MinimumUpdater = FullUpdaterVersion + 1
	// Signed future metadata is recognizable, but installation must request an image.
	if err := validateManifest(m); err != nil {
		t.Fatal(err)
	}
	err := (&fullUpdater{root: t.TempDir()}).check(m)
	if err == nil || !strings.Contains(err.Error(), "full SD image") {
		t.Fatalf("wrong boundary: %v", err)
	}
}

func TestFullUpdateCannotRevertSameCapabilityHelperSequence(t *testing.T) {
	m := fullFixture()
	m.Full.TargetUpdaterSequence = 4
	root := t.TempDir()
	base := filepath.Join(root, "kvmapp/.os-update")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	current := UpdaterState{Capability: UpdaterCapability, Version: "1.0.0-beta.8", Sequence: 5, ID: strings.Repeat("b", 64)}
	if err := writeJSON(filepath.Join(base, "updater.json"), current); err != nil {
		t.Fatal(err)
	}
	err := (&fullUpdater{root: root}).check(m)
	if err == nil || !strings.Contains(err.Error(), "revert") {
		t.Fatalf("newer helper was not protected: %v", err)
	}
	if err := validateManifest(func() Manifest { bad := m; bad.Full.TargetUpdaterSHA256 = ""; return bad }()); err == nil {
		t.Fatal("accepted target updater metadata without helper digest")
	}
}
