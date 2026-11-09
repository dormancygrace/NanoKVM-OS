package storage

import (
	"os"
	"path/filepath"

	"testing"
)

// MCP's virtual_media mounts through MountImageFile: the same refusals as the
// page while remote media or network boot own the virtual disk, before the
// disk is touched, and the page still gets 409 for them.

// Free: the mount goes on and meets the inactive USB storage.

func TestImageFileAcceptsOnlyListedImages(t *testing.T) {
	root, outside := imageFixture(t)

	for _, name := range []string{"disk.iso", "sub/disk.IMG"} {
		path := filepath.Join(root, name)
		if got, err := imageFile(path); err != nil || got != path {
			t.Errorf("imageFile(%q) = %q, %v; want the file", path, got, err)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "notes.txt"),
		filepath.Join(root, "link.img"),
		filepath.Join(root, "dir.iso"),
		filepath.Join(root, "missing.iso"),
		root + "/../" + filepath.Base(outside) + "/secret.img",
		filepath.Join(root, ".nanokvm", "user-disk.img"),
		"/dev/mmcblk0",
		"",
	} {
		if got, err := imageFile(path); err == nil {
			t.Errorf("imageFile(%q) = %q; want an error", path, got)
		}
	}
}

// imageFixture points imageDirectory at a new directory with two images and
// entries that are not mountable images: a text file, a symbolic link to an
// image outside the directory, a directory with an image suffix, and the
// user disk in its hidden directory.
func imageFixture(t *testing.T) (root, outside string) {
	t.Helper()
	root = t.TempDir()
	original := imageDirectory
	imageDirectory = root
	t.Cleanup(func() { imageDirectory = original })
	outside = t.TempDir()
	for _, name := range []string{"disk.iso", "sub/disk.IMG", "notes.txt", ".nanokvm/user-disk.img"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.img"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.img"), filepath.Join(root, "link.img")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir.iso"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, outside
}
