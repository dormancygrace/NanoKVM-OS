package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const edgeRepositories = `https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb
https://dl-cdn.alpinelinux.org/alpine/v3.24/main
https://dl-cdn.alpinelinux.org/alpine/v3.24/community
@edgecommunity https://dl-cdn.alpinelinux.org/alpine/edge/community
`

const edgeWithoutLine = `https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb
https://dl-cdn.alpinelinux.org/alpine/v3.24/main
https://dl-cdn.alpinelinux.org/alpine/v3.24/community
`

func edgeFixture(t *testing.T, repositories, world, installed string) string {
	t.Helper()
	oldRepos, oldWorld, oldLock, oldDB := repositoriesPath, worldPath, apkDBLockPath, installedDBPath
	t.Cleanup(func() {
		repositoriesPath, worldPath, apkDBLockPath, installedDBPath = oldRepos, oldWorld, oldLock, oldDB
	})
	dir := t.TempDir()
	repositoriesPath, worldPath = filepath.Join(dir, "repositories"), filepath.Join(dir, "world")
	apkDBLockPath, installedDBPath = filepath.Join(dir, "lock"), filepath.Join(dir, "installed")
	for path, data := range map[string]string{repositoriesPath: repositories, worldPath: world, installedDBPath: installed} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEdgeMigration(t *testing.T) {
	edgeFixture(t, edgeRepositories,
		"alpine-baselayout\nnanokvm-release\ntailscale@edgecommunity\ntailscale-openrc@edgecommunity\nnetbird@edgecommunity=0.78.2-r0\nfoo@edgecommunity\n",
		"P:tailscale\nV:1.102.5-r0\n\nP:tailscale-openrc\nV:1.102.5-r0\n\nP:netbird\nV:0.78.2-r0\n\n")
	if err := MigrateEdgeRepository(); err != nil {
		t.Fatal(err)
	}
	// Installed ones keep their version or newer; an explicit constraint stays;
	// an entry that is not installed just loses the tag.
	want := "alpine-baselayout\nnanokvm-release\ntailscale>=1.102.5-r0\ntailscale-openrc>=1.102.5-r0\nnetbird=0.78.2-r0\nfoo\n"
	if got := read(t, worldPath); got != want {
		t.Fatalf("world=%q", got)
	}
	if got := read(t, repositoriesPath); got != edgeWithoutLine {
		t.Fatalf("repositories=%q", got)
	}
	// Done: a second run changes nothing.
	if err := MigrateEdgeRepository(); err != nil || read(t, worldPath) != want {
		t.Fatalf("second run: %v", err)
	}
}

func TestEdgeMigrationWithoutEdgePackages(t *testing.T) {
	edgeFixture(t, edgeRepositories, "alpine-baselayout\nnanokvm-release\n", "")
	if err := MigrateEdgeRepository(); err != nil {
		t.Fatal(err)
	}
	if read(t, worldPath) != "alpine-baselayout\nnanokvm-release\n" || read(t, repositoriesPath) != edgeWithoutLine {
		t.Fatal("unexpected change")
	}
}

func TestEdgeMigrationWaitsForApk(t *testing.T) {
	edgeFixture(t, edgeRepositories, "tailscale@edgecommunity\n", "P:tailscale\nV:1.102.5-r0\n\n")
	f, err := os.OpenFile(apkDBLockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err = MigrateEdgeRepository(); err != errEdgeBusy {
		t.Fatalf("err=%v while apk holds its lock", err)
	}
	if read(t, worldPath) != "tailscale@edgecommunity\n" || read(t, repositoriesPath) != edgeRepositories {
		t.Fatal("changed while apk ran")
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err = MigrateEdgeRepository(); err != nil || read(t, worldPath) != "tailscale>=1.102.5-r0\n" {
		t.Fatalf("after apk: %v %q", err, read(t, worldPath))
	}
}

func TestEdgeMigrationKeepsUnknownForms(t *testing.T) {
	edgeFixture(t, edgeRepositories, "Odd@edgecommunity\n", "")
	if err := MigrateEdgeRepository(); err == nil || !strings.Contains(err.Error(), "unknown form") {
		t.Fatalf("err=%v", err)
	}
	if read(t, repositoriesPath) != edgeRepositories {
		t.Fatal("removed the line although the world still needs the tag")
	}
}

func TestEdgeMigrationNothingToDo(t *testing.T) {
	edgeFixture(t, edgeWithoutLine, "tailscale\n", "")
	if err := MigrateEdgeRepository(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(apkDBLockPath); !os.IsNotExist(err) {
		t.Fatal("took apk's lock without a reason")
	}
}
