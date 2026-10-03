package router

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledBinaryTracksInstallRemovalWithoutServiceChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addon")
	check := func(want bool) {
		t.Helper()
		got, err := installedBinary(path)
		if err != nil || got != want {
			t.Fatalf("installed=%v, err=%v, want=%v", got, err, want)
		}
	}
	check(false)
	if err := os.WriteFile(path, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	check(false)
}
