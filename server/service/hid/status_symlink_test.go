package hid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyModeFilePreservesCompatibilitySymlink(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "source")
	target := filepath.Join(d, "legacy")
	link := filepath.Join(d, "S03usbdev")
	if err := os.WriteFile(src, []byte("new script"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old script"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("legacy", link); err != nil {
		t.Fatal(err)
	}
	if err := copyModeFileTo(src, link); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(link); err != nil || got != "legacy" {
		t.Fatalf("link changed: %q %v", got, err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "new script" {
		t.Fatalf("target not updated: %q %v", got, err)
	}
}
func TestCopyModeFileRejectsDanglingSymlink(t *testing.T) {
	d := t.TempDir()
	link := filepath.Join(d, "S03usbdev")
	if err := os.Symlink("missing", link); err != nil {
		t.Fatal(err)
	}
	if err := copyModeFileTo(filepath.Join(d, "source"), link); err == nil {
		t.Fatal("accepted dangling link")
	}
	if _, err := os.Readlink(link); err != nil {
		t.Fatal("link replaced")
	}
}
