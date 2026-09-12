package osupdate

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

func kernelFixture(t *testing.T) (*systemUpdater, string, *Bundle) {
	t.Helper()
	u, _, _ := systemFixture(t)
	for _, p := range []string{"/boot", "/proc/sys/kernel"} {
		if err := os.MkdirAll(u.at(p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(u.at("/boot/boot.sd"), []byte("old FIT"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.at("/proc/sys/kernel/osrelease"), []byte("7.2.4-old"), 0644); err != nil {
		t.Fatal(err)
	}
	fit := make([]byte, 64)
	for off, n := range map[int]uint32{0: 0xd00dfeed, 4: 64, 8: 40, 12: 56, 32: 8, 36: 16} {
		binary.BigEndian.PutUint32(fit[off:off+4], n)
	}
	file, key := fixture(t, func(m *Manifest) {
		m.Format = 3
		m.Kernel = &KernelUpdate{Release: "7.2.4-new", SystemBase: digest([]byte("new foundation"))}
	}, nil,
		testPackageFile{kernelBootPath, fit, 0644, "", false},
		testPackageFile{"rootfs/usr/lib/modules/7.2.4-new/modules.dep", []byte("kernel/test.ko:\n"), 0644, "", false},
		testPackageFile{"rootfs/usr/lib/modules/7.2.4-new/modules.builtin", []byte("builtin.ko\n"), 0644, "", false},
		testPackageFile{"rootfs/usr/lib/modules/7.2.4-new/kernel/test.ko", []byte("test module"), 0644, "", false})
	b, err := verifyWithKey(file, key)
	if err != nil {
		t.Fatal(err)
	}
	return u, file, b
}
func TestKernelPackageStagesBootThenInstallsMatchingModules(t *testing.T) {
	u, file, b := kernelFixture(t)
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	if !exists(u.at("/usr/lib/modules/7.2.4-new/modules.dep")) {
		t.Fatal("matching modules must exist before the new kernel boots")
	}
	if err := u.boot(); err == nil {
		t.Fatal("old kernel accepted")
	}
	expectContent(t, u.at("/kvmapp/server/NanoKVM-Server"), "old app")
	if err := os.WriteFile(u.at("/proc/sys/kernel/osrelease"), []byte("7.2.4-new"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/usr/lib/modules/7.2.4-new/kernel/test.ko"), "test module")
	if err := u.confirm(func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/etc/nkos-system-base"), b.Manifest.Kernel.SystemBase+"\n")
}
func TestKernelPackageRejectsUnsafePathsAndMissingModules(t *testing.T) {
	_, _, b := kernelFixture(t)
	for _, name := range []string{"rootfs/boot/fip.bin", "rootfs/boot/uEnv.txt", "rootfs/usr/lib/modules/7.2.4-old/test.ko", "rootfs/usr/lib/modules/7.2.4-new/../../evil"} {
		m := b.Manifest
		m.Files = append(append([]Entry{}, m.Files...), Entry{Path: name, Size: 1, Mode: 0644, SHA256: digest([]byte("x"))})
		if validateManifest(m) == nil {
			t.Errorf("accepted %s", name)
		}
	}
	m := b.Manifest
	m.Format = 2
	if validateManifest(m) == nil {
		t.Fatal("format 2 accepted kernel")
	}
	m = b.Manifest
	m.Files = append([]Entry{}, m.Files[:len(m.Files)-2]...)
	if validateManifest(m) == nil {
		t.Fatal("missing module metadata accepted")
	}
}
func TestKernelInterruptedUpdateDoesNotRollBackUserspaceUnderNewKernel(t *testing.T) {
	u, file, b := kernelFixture(t)
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.at("/proc/sys/kernel/osrelease"), []byte("7.2.4-new"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	if err := u.boot(); err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("expected explicit recovery failure, got %v", err)
	}
	expectContent(t, u.at("/usr/lib/modules/7.2.4-new/kernel/test.ko"), "test module")
}
func TestKernelRejectsMalformedFITBeforePublishing(t *testing.T) {
	u, _, b := kernelFixture(t)
	if err := os.MkdirAll(u.file("system-stage/rootfs/boot"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.file("system-stage/"+kernelBootPath), []byte("not a FIT"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := u.publishKernel(systemTransaction{Kernel: b.Manifest.Kernel, PackageFiles: b.Manifest.Files}); err == nil {
		t.Fatal("bad FIT accepted")
	}
	expectContent(t, u.at("/boot/boot.sd"), "old FIT")
	if err := u.checkKernelTarget(b.Manifest); err == nil {
		t.Fatal("unmounted boot directory accepted")
	}
}

func TestKernelBootMountsPartitionBeforeReadingFIT(t *testing.T) {
	u, file, b := kernelFixture(t)
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.at("/proc/sys/kernel/osrelease"), []byte(b.Manifest.Kernel.Release), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(u.at("/boot/boot.sd"), u.at("/boot-not-mounted")); err != nil {
		t.Fatal(err)
	}
	called := false
	u.mountBoot = func() error { called = true; return os.Rename(u.at("/boot-not-mounted"), u.at("/boot/boot.sd")) }
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("boot verification did not mount the partition")
	}
}
