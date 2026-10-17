package osupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"NanoKVM-Server/internal/apkrun"
)

const communityRepositories = `https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb
https://dl-cdn.alpinelinux.org/alpine/v3.24/main
https://dl-cdn.alpinelinux.org/alpine/v3.24/community
`

const communityOff = `https://nkos.pesin.pro/repos/nanokvm/riscv64/Packages.adb
https://dl-cdn.alpinelinux.org/alpine/v3.24/main
`

// communityFixture points the repositories, locks and marker at a temporary
// directory and installs a fake apk whose `list --orphaned` prints $WITH or
// $WITHOUT, depending on whether the repositories file lists community, and
// $WARN on stderr.
func communityFixture(t *testing.T, repositories string) string {
	t.Helper()
	dir := edgeFixture(t, repositories, "", "")
	oldMarker, oldTmp, oldExec, oldLock, oldApkLock := communityMarkerPath, communityTmpDir, apkExecutable, updateLock, apkrun.LockPath
	t.Cleanup(func() {
		communityMarkerPath, communityTmpDir, apkExecutable, updateLock, apkrun.LockPath = oldMarker, oldTmp, oldExec, oldLock, oldApkLock
	})
	communityMarkerPath = filepath.Join(dir, "kvm", "apk-community.chosen")
	communityTmpDir = t.TempDir()
	apkrun.LockPath = filepath.Join(dir, "apk.lock")
	updateLock = func() (*os.File, error) { return os.Open(dir) }
	apkExecutable = filepath.Join(dir, "apk")
	script := `#!/bin/sh
[ "$1" = --repositories-file ] && [ "$3" = list ] && [ "$4" = --orphaned ] || exit 99
if [ -n "$CHECK_DB_LOCK" ]; then
    if flock -n "$CHECK_DB_LOCK" true; then echo 'database unlocked during query' >&2; exit 98; fi
fi
[ -n "$WARN" ] && echo "$WARN" >&2
if grep -q community "$2"; then printf '%b' "$WITH"; else printf '%b' "$WITHOUT"; fi
`
	if err := os.WriteFile(apkExecutable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WITH", "")
	t.Setenv("WITHOUT", "")
	t.Setenv("WARN", "")
	t.Setenv("CHECK_DB_LOCK", "")
	return dir
}

func orphanLine(name string) string {
	return name + "-1.2.3-r0 riscv64 {" + name + "} (MIT) [installed]\n"
}

func TestCommunityURLAndLine(t *testing.T) {
	mirror := "https://mirror.test/alpine/v3.24/main/\n@edgecommunity https://x.test/alpine/edge/community\n"
	if got := communityURL(mirror); got != "https://mirror.test/alpine/v3.24/community" {
		t.Fatal(got)
	}
	if got := communityURL("@tag https://x.test/alpine/v3.24/main\nhttps://custom.test/repo\n"); got != "" {
		t.Fatal(got)
	}
	url := communityURL(communityOff)
	if got := withCommunity(communityOff, url, true); got != communityRepositories {
		t.Fatalf("%q", got)
	}
	if got := withCommunity(communityRepositories, url, false); got != communityOff {
		t.Fatalf("%q", got)
	}
	if got := withCommunity(communityRepositories, url, true); got != communityRepositories {
		t.Fatalf("enabling twice: %q", got)
	}
}

func TestSetCommunityRefusesWhileNeeded(t *testing.T) {
	dir := communityFixture(t, communityRepositories)
	t.Setenv("WITHOUT", orphanLine("tailscale")+orphanLine("netbird-openrc"))
	change, err := SetCommunity(false)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Enabled || !reflect.DeepEqual(change.Needed, []string{"netbird-openrc", "tailscale"}) {
		t.Fatal(change)
	}
	if got := read(t, filepath.Join(dir, "repositories")); got != communityRepositories {
		t.Fatalf("repositories changed: %q", got)
	}
	if _, err := os.Stat(communityMarkerPath); err == nil {
		t.Fatal("a refused change was recorded as chosen")
	}
}

func TestSetCommunitySwitches(t *testing.T) {
	dir := communityFixture(t, communityRepositories)
	change, err := SetCommunity(false)
	if err != nil || change.Enabled {
		t.Fatal(change, err)
	}
	if got := read(t, filepath.Join(dir, "repositories")); got != communityOff {
		t.Fatalf("%q", got)
	}
	if status, _ := GetCommunity(); status.Enabled || !status.Available {
		t.Fatal(status)
	}
	if _, err := os.Stat(communityMarkerPath); err != nil {
		t.Fatal("switching off is not recorded:", err)
	}
	if change, err = SetCommunity(true); err != nil || !change.Enabled {
		t.Fatal(change, err)
	}
	if got := read(t, filepath.Join(dir, "repositories")); got != communityRepositories {
		t.Fatalf("%q", got)
	}
}

func TestSetCommunityKeepsLineOnUnknownIndexes(t *testing.T) {
	dir := communityFixture(t, communityRepositories)
	t.Setenv("WARN", "WARNING: opening from cache https://dl-cdn.alpinelinux.org/alpine/v3.24/main: No such file or directory")
	t.Setenv("WITHOUT", orphanLine("busybox"))
	if _, err := SetCommunity(false); !errors.Is(err, errCommunityUnknown) {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "repositories")); got != communityRepositories {
		t.Fatalf("%q", got)
	}
}

func TestEnableCommunityLocked(t *testing.T) {
	dir := communityFixture(t, communityOff)
	if err := EnableCommunityLocked(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "repositories")); got != communityRepositories {
		t.Fatalf("%q", got)
	}
	if _, err := os.Stat(communityMarkerPath); err != nil {
		t.Fatal(err)
	}
}

func TestCommunityMigration(t *testing.T) {
	ctx := context.Background()
	t.Run("nothing needs it", func(t *testing.T) {
		dir := communityFixture(t, communityRepositories)
		if err := MigrateCommunityDefault(ctx); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(dir, "repositories")); got != communityOff {
			t.Fatalf("%q", got)
		}
		if _, err := os.Stat(communityMarkerPath); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a package needs it", func(t *testing.T) {
		dir := communityFixture(t, communityRepositories)
		t.Setenv("WITHOUT", orphanLine("tailscale"))
		if err := MigrateCommunityDefault(ctx); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(dir, "repositories")); got != communityRepositories {
			t.Fatalf("%q", got)
		}
		if _, err := os.Stat(communityMarkerPath); err != nil {
			t.Fatal("keeping it is not recorded:", err)
		}
	})
	t.Run("waits for the NanoKVM replacements", func(t *testing.T) {
		dir := communityFixture(t, communityRepositories)
		t.Setenv("WITHOUT", orphanLine("exfatprogs")+orphanLine("i2c-tools"))
		if err := MigrateCommunityDefault(ctx); !errors.Is(err, errCommunityHold) {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(dir, "repositories")); got != communityRepositories {
			t.Fatalf("%q", got)
		}
		if _, err := os.Stat(communityMarkerPath); err == nil {
			t.Fatal("decided before the upgrade")
		}
	})
	t.Run("offline", func(t *testing.T) {
		communityFixture(t, communityRepositories)
		t.Setenv("WARN", "WARNING: fetching https://dl-cdn.alpinelinux.org/alpine/v3.24/community: network error")
		if err := MigrateCommunityDefault(ctx); !errors.Is(err, errCommunityUnknown) {
			t.Fatal(err)
		}
		if _, err := os.Stat(communityMarkerPath); err == nil {
			t.Fatal("decided without indexes")
		}
	})
	t.Run("chosen before", func(t *testing.T) {
		dir := communityFixture(t, communityRepositories)
		if err := markCommunityChosen(); err != nil {
			t.Fatal(err)
		}
		t.Setenv("WITH", "unparsable\n")
		if err := MigrateCommunityDefault(ctx); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(dir, "repositories")); got != communityRepositories {
			t.Fatalf("%q", got)
		}
	})
	t.Run("custom repositories", func(t *testing.T) {
		dir := communityFixture(t, "https://custom.test/repo\n")
		if err := MigrateCommunityDefault(ctx); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(dir, "repositories")); !strings.Contains(got, "custom.test") {
			t.Fatalf("%q", got)
		}
	})
}

func TestCommunityMigrationHonorsChoiceMadeBeforeLock(t *testing.T) {
	dir := communityFixture(t, communityRepositories)
	updateLock = func() (*os.File, error) {
		// A manual enable finished after the migration's initial marker check.
		if err := markCommunityChosen(); err != nil {
			return nil, err
		}
		return os.Open(dir)
	}
	if err := MigrateCommunityDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, repositoriesPath); got != communityRepositories {
		t.Fatalf("migration overrode the user's choice: %q", got)
	}
}

func TestCommunityLocksDatabaseAcrossQueries(t *testing.T) {
	for _, migration := range []bool{false, true} {
		t.Run(fmt.Sprint("migration=", migration), func(t *testing.T) {
			communityFixture(t, communityRepositories)
			t.Setenv("CHECK_DB_LOCK", apkDBLockPath)
			var err error
			if migration {
				err = MigrateCommunityDefault(context.Background())
			} else {
				_, err = SetCommunity(false)
			}
			if err != nil {
				t.Fatal(err)
			}
			if read(t, repositoriesPath) != communityOff {
				t.Fatal("repository not disabled")
			}
			f, err := os.OpenFile(apkDBLockPath, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal("database lock leaked:", err)
			}
		})
	}
}

func TestCommunityWaitsForExternalApk(t *testing.T) {
	for _, migration := range []bool{false, true} {
		t.Run(fmt.Sprint("migration=", migration), func(t *testing.T) {
			communityFixture(t, communityRepositories)
			f, err := os.OpenFile(apkDBLockPath, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			if migration {
				err = MigrateCommunityDefault(context.Background())
			} else {
				_, err = SetCommunity(false)
			}
			if !errors.Is(err, errEdgeBusy) {
				t.Fatalf("got %v, want busy", err)
			}
			if read(t, repositoriesPath) != communityRepositories {
				t.Fatal("repositories changed during external transaction")
			}
			if _, err := os.Stat(communityMarkerPath); !os.IsNotExist(err) {
				t.Fatal("decision recorded during external transaction:", err)
			}
		})
	}
}
