package osupdate

import (
	"os"
	"path/filepath"

	"testing"

	"NanoKVM-Server/internal/apkrun"
)

const communityOff = `https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb
https://dl-cdn.alpinelinux.org/alpine/v3.24/main
`

// communityFixture points the repositories and locks at a temporary directory
// and installs a fake apk whose `list --orphaned` prints $WITH or $WITHOUT,
// depending on whether the repositories file lists community, and $WARN on
// stderr.
func communityFixture(t *testing.T, repositories string) string {
	t.Helper()
	oldRepos, oldDBLock, oldTmp, oldExec, oldLock, oldApkLock := repositoriesPath, apkDBLockPath, communityTmpDir, apkExecutable, updateLock, apkrun.LockPath
	t.Cleanup(func() {
		repositoriesPath, apkDBLockPath, communityTmpDir, apkExecutable, updateLock, apkrun.LockPath = oldRepos, oldDBLock, oldTmp, oldExec, oldLock, oldApkLock
	})
	dir := t.TempDir()
	repositoriesPath, apkDBLockPath = filepath.Join(dir, "repositories"), filepath.Join(dir, "lock")
	if err := os.WriteFile(repositoriesPath, []byte(repositories), 0644); err != nil {
		t.Fatal(err)
	}
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
