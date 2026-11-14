package helperdisk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "helper-disk.img")
	for _, step := range []struct {
		image   string
		written bool
	}{{"one", true}, {"one", false}, {"two", true}} {
		if written, err := Publish(path, []byte(step.image)); err != nil || written != step.written {
			t.Fatalf("image %q: written %v, err %v", step.image, written, err)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != "two" {
		t.Fatalf("image on disk: %q", data)
	}
	// A write that cannot happen is an error, also over a readable image.
	if err := os.Chmod(filepath.Dir(path), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Dir(path), 0o755)
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	if written, err := Publish(path, []byte("three")); err == nil || written {
		t.Fatalf("read-only directory: written %v, err %v", written, err)
	}
}
