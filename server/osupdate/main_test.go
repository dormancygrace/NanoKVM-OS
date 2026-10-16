package osupdate

import (
	"os"
	"path/filepath"
	"testing"

	"NanoKVM-Server/internal/apkrun"
)

// The fake apk runs take the apk lock; keep it out of the system's /run.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "osupdate-apk-lock-")
	if err != nil {
		panic(err)
	}
	apkrun.LockPath = filepath.Join(dir, "apk.lock")
	// The update notice that package operations refresh: never the host's.
	repositoriesPath = filepath.Join(dir, "repositories")
	noticeCacheDir = filepath.Join(dir, "notice-cache")
	noticeDisabledPath = filepath.Join(dir, "update-check.disabled")
	installedDBPath = filepath.Join(dir, "installed")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
