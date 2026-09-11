package osupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func systemFixture(t *testing.T) (*systemUpdater, string, *Bundle) {
	t.Helper()
	u := &systemUpdater{root: t.TempDir(), bootID: func() string { return "boot-A" }}
	for _, d := range []string{Base, "/usr/bin", "/usr/sbin", "/etc", "/kvmapp/server/dl_lib", "/kvmapp/server/web"} {
		if err := os.MkdirAll(u.at(d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for p, v := range map[string]string{Helper: "static recovery helper", "/usr/bin/nano": "old nano", "/etc/chrony.conf": "user's NTP servers", "/kvmapp/version": "1.0.0-beta.3\n", "/kvmapp/server/NanoKVM-Server": "old app", "/kvmapp/server/web/index.html": "old page", "/kvmapp/server/dl_lib/libkvm.so": "native"} {
		if err := os.WriteFile(u.at(p), []byte(v), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(u.file("installed.json"), Installed{Version: "1.0.0-beta.3", Sequence: 6}); err != nil {
		t.Fatal(err)
	}
	file, key := fixture(t, func(m *Manifest) { m.Version = "1.0.0-beta.4"; m.Sequence = 7 }, nil,
		testPackageFile{"rootfs/usr/bin/nano", []byte("new nano"), 0755, "", false},
		testPackageFile{"rootfs/usr/share/zoneinfo/UTC", []byte("tz data"), 0644, "", false},
		testPackageFile{"rootfs/etc/chrony.conf", []byte("new default servers"), 0644, "", true},
		testPackageFile{"rootfs/usr/bin/editor", []byte("nano"), 0644, "nano", false})
	b, err := verifyWithKey(file, key)
	if err != nil {
		t.Fatal(err)
	}
	return u, file, b
}
func expectContent(t *testing.T, p, want string) {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil || string(b) != want {
		t.Fatalf("%s: got %q, error=%v, want %q", p, b, e, want)
	}
}
func TestSystemUpdateCommitAndPreserveConfiguration(t *testing.T) {
	u, file, b := systemFixture(t)
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/usr/bin/nano"), "old nano")
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/usr/bin/nano"), "new nano")
	expectContent(t, u.at("/etc/chrony.conf"), "user's NTP servers")
	expectContent(t, u.at("/usr/bin/editor"), "new nano")
	expectContent(t, u.at("/kvmapp/server/web/index.html"), "test page")
	if err := u.confirm(func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if exists(u.file("system.json")) {
		t.Fatal("committed journal remains")
	}
	expectContent(t, u.at("/kvmapp/version"), "1.0.0-beta.4\n")
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/usr/bin/nano"), "new nano")
	// A later update must not trip on retained payload/rollback directories.
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
}
func TestUnconfirmedBootRestoresSystemAndApplication(t *testing.T) {
	u, file, b := systemFixture(t)
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	if err := u.confirm(func() bool { return false }); err == nil {
		t.Fatal("unhealthy application committed")
	}
	u.bootID = func() string { return "boot-B" }
	if err := u.confirm(func() bool { return true }); err == nil {
		t.Fatal("confirmed from wrong boot")
	}
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/usr/bin/nano"), "old nano")
	expectContent(t, u.at("/kvmapp/server/web/index.html"), "old page")
	expectContent(t, u.at("/etc/chrony.conf"), "user's NTP servers")
	if exists(u.at("/usr/share/zoneinfo/UTC")) || exists(u.at("/usr/bin/editor")) {
		t.Fatal("new system files not removed")
	}
	expectContent(t, u.at("/kvmapp/version"), "1.0.0-beta.3\n")
}
func TestInterruptedFileReplacementRollsBack(t *testing.T) {
	u, file, b := systemFixture(t)
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	tx, err := u.read()
	if err != nil {
		t.Fatal(err)
	}
	tx.Phase = "applying"
	if err = u.save(tx); err != nil {
		t.Fatal(err)
	}
	if err = replaceLinked(u.file("system-stage/rootfs/usr/bin/nano"), u.at("/usr/bin/nano")); err != nil {
		t.Fatal(err)
	}
	if err = u.boot(); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/usr/bin/nano"), "old nano")
	expectContent(t, u.at("/kvmapp/server/web/index.html"), "old page")
}
func TestSystemRejectsSymlinkParentsAndProtectedPaths(t *testing.T) {
	for _, name := range []string{"/boot/boot.sd", "/lib/libc.so", "/usr/lib/modules/7/driver.ko", "/etc/shadow", "/etc/kvm/server.yaml", "/etc/init.d/S00nkos-system-update", "/etc/init.d/S99nkos-system-confirm", "/usr/bin/../../etc/passwd"} {
		if systemPathAllowed(name) {
			t.Fatalf("protected path allowed: %s", name)
		}
	}
	u, file, b := systemFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, u.at("/usr/share")); err != nil {
		t.Fatal(err)
	}
	if err := u.stage(file, b); err == nil {
		t.Fatal("followed system parent symlink")
	}
	if exists(filepath.Join(outside, "zoneinfo")) {
		t.Fatal("wrote outside managed root")
	}
}
func TestCorruptStagedSystemFileRollsBack(t *testing.T) {
	u, file, b := systemFixture(t)
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.file("system-stage/rootfs/usr/bin/nano"), []byte("damaged"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/usr/bin/nano"), "old nano")
	var result Result
	if err := readJSON(u.file("result.json"), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "rolled-back" {
		t.Fatal(result)
	}
}
func TestSystemAttributesAndParentsAreSignedAndRestricted(t *testing.T) {
	for _, change := range []func(*Manifest){
		func(m *Manifest) { m.Files[0].Preserve = true },
		func(m *Manifest) { m.Files[2].Link = "../../../etc/shadow" },
		func(m *Manifest) { m.Files[2].Mode = 04755 },
		func(m *Manifest) { m.Files[2].Path = "rootfs/usr/bin"; m.Files[3].Path = "rootfs/usr/bin/child" },
	} {
		file, key := fixture(t, change, nil, testPackageFile{"rootfs/usr/bin/x", []byte("a"), 0755, "", false}, testPackageFile{"rootfs/usr/bin/y", []byte("b"), 0755, "", false})
		if _, err := verifyWithKey(file, key); err == nil {
			t.Fatal("invalid system entry accepted")
		}
	}
	if !ValidAssetURL("https://github.com/" + Repository + "/releases/download/v1/NanoKVM-OS-update.nkos") {
		t.Fatal("system asset URL rejected")
	}
	if validSystemEntry(Entry{Path: "rootfs/etc/chrony.conf", Mode: 0644}) {
		t.Fatal("configuration overwrite accepted")
	}
}

func TestSystemRemovalsRestoreOnFailedBoot(t *testing.T) {
	u, file, b := systemFixture(t)
	b.Manifest.Remove = []string{"rootfs/usr/bin/obsolete"}
	if err := os.WriteFile(u.at("/usr/bin/obsolete"), []byte("old tool"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	if exists(u.at("/usr/bin/obsolete")) {
		t.Fatal("obsolete tool remains")
	}
	u.bootID = func() string { return "boot-B" }
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/usr/bin/obsolete"), "old tool")
	if validSystemRemoval("rootfs" + Helper) {
		t.Fatal("helper removal permitted")
	}
}
func TestCorruptStagedWebFileDoesNotCommit(t *testing.T) {
	u, file, b := systemFixture(t)
	if err := u.stage(file, b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.file("system-stage/web/index.html"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := u.boot(); err != nil {
		t.Fatal(err)
	}
	expectContent(t, u.at("/kvmapp/server/web/index.html"), "old page")
	expectContent(t, u.at("/usr/bin/nano"), "old nano")
}
