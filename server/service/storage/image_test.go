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

// Only a change of the drive's type or name asks for a reconnection, and a
// mount that failed after the change leaves no trace of it: the next one
// must see the change again.
func TestSetDriveReportsAChangedDrive(t *testing.T) {
	dir := t.TempDir()
	for _, file := range []*string{&roFlag, &cdromFlag, &inquiryString} {
		original := *file
		t.Cleanup(func() { *file = original })
		*file = filepath.Join(dir, filepath.Base(original))
	}
	for _, step := range []struct {
		cdrom         bool
		name          string
		changed, fail bool
	}{{false, "Disk", true, false}, {false, "Disk", false, false}, {true, "CD", true, true}, {true, "CD", true, false},
		{true, "CD", false, false}, {true, "Remote ISO", true, false}} {
		changed, restore, err := setDrive(step.cdrom, step.name)
		if err != nil || changed != step.changed {
			t.Errorf("setDrive(%v, %q) = %v, %v; want %v", step.cdrom, step.name, changed, err, step.changed)
		}
		if step.fail {
			restore()
		}
	}
	// Failing halfway (the name cannot be written) undoes the first half.
	inquiryString = filepath.Join(dir, "missing", "inquiry_string")
	if _, _, err := setDrive(false, "Disk"); err == nil {
		t.Error("setDrive wrote a name that cannot be written")
	}
	if flag, _ := os.ReadFile(cdromFlag); string(flag) != "1" {
		t.Errorf("a failed setDrive left cdrom=%q", flag)
	}
}
