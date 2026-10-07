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
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
