package osupdate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"NanoKVM-Server/internal/apkrun"
)

const communityRepositories = `https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb
https://dl-cdn.alpinelinux.org/alpine/v3.24/main
https://dl-cdn.alpinelinux.org/alpine/v3.24/community
`

const communityOff = `https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb
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
