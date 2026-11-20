package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SetChannel switches only a list with exactly one official NanoKVM line and
// no other NanoKVM line; anything else is a custom setup and stays as it is.
func TestChannelRejectsCustomAndAmbiguousRepositories(t *testing.T) {
	const official = "https://dormancygrace.github.io/NanoKVM-OS/alpine/v3.24/riscv64/Packages.adb\n" +
		"https://dl-cdn.alpinelinux.org/alpine/v3.24/main\n"
	for _, data := range []string{"https://example.test/nanokvm/repo\n", official + official,
		official + "https://example.test/nanokvm/repo\n", "@testing " + official} {
		t.Run(data, func(t *testing.T) {
			dir := t.TempDir()
			oldRepos, oldDBLock, oldState, oldLock := repositoriesPath, apkDBLockPath, apkStateDir, updateLock
			t.Cleanup(func() {
				repositoriesPath, apkDBLockPath, apkStateDir, updateLock = oldRepos, oldDBLock, oldState, oldLock
			})
			repositoriesPath, apkDBLockPath = filepath.Join(dir, "repositories"), filepath.Join(dir, "lock")
			apkStateDir = filepath.Join(dir, "state")
			updateLock = func() (*os.File, error) { return os.Open(dir) }
			if err := os.WriteFile(repositoriesPath, []byte(data), 0644); err != nil {
				t.Fatal(err)
			}
			if got := GetChannel(); got.Available || got.Channel != "custom" {
				t.Fatal("custom repository accepted")
			}
			if err := SetChannel("beta"); err == nil || !strings.Contains(err.Error(), "one official NanoKVM OS repository") {
				t.Fatalf("changed custom repository: %v", err)
			}
			if got, _ := os.ReadFile(repositoriesPath); string(got) != data {
				t.Fatal("modified")
			}
		})
	}
}
