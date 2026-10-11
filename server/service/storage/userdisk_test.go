package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupUserDisk(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	saveDir, saveImage, saveLun, saveFlag, saveFormat := userDiskDir, userDiskImage, userDiskLun, userDiskFlag, userDiskFormat
	userDiskDir = filepath.Join(dir, ".nanokvm")
	userDiskImage = filepath.Join(userDiskDir, "user-disk.img")
	userDiskLun = filepath.Join(dir, "lun.2-file")
	userDiskFlag = filepath.Join(dir, "usb.userdisk")
	t.Cleanup(func() {
		userDiskDir, userDiskImage, userDiskLun, userDiskFlag, userDiskFormat = saveDir, saveImage, saveLun, saveFlag, saveFormat
	})
	return dir
}

func TestCreateUserDiskBuildsTheImageUnderATemporaryName(t *testing.T) {
	setupUserDisk(t)
	var formatted string
	userDiskFormat = func(path string, _ int) error {
		formatted = path
		return nil
	}
	if err := createUserDisk(64); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(formatted), ".user-disk-") {
		t.Fatalf("formatted %s", formatted)
	}
	info, err := os.Stat(userDiskImage)
	if err != nil || info.Size() != 64*1024*1024 || info.Mode().Perm() != 0644 {
		t.Fatalf("image: %v %v", info, err)
	}
	state := userDiskState()
	if !state.Exists || state.SizeMiB != 64 || state.Enabled || state.Attached {
		t.Fatalf("state %+v", state)
	}
	for _, size := range []int{0, 32, 63, 8192} {
		if err := createUserDisk(size); err == nil {
			t.Fatalf("%d MiB accepted", size)
		}
	}
}

func TestCreateUserDiskKeepsTheOldImageWhenFormattingFails(t *testing.T) {
	setupUserDisk(t)
	userDiskFormat = func(string, int) error { return nil }
	if err := createUserDisk(64); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(userDiskImage)
	userDiskFormat = func(string, int) error { return os.ErrPermission }
	if err := createUserDisk(128); err == nil {
		t.Fatal("a failed format passed")
	}
	after, _ := os.Stat(userDiskImage)
	if after.Size() != before.Size() {
		t.Fatal("the old image was replaced")
	}
	entries, _ := os.ReadDir(userDiskDir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestUserDiskStateFollowsTheMarkerAndTheLun(t *testing.T) {
	setupUserDisk(t)
	userDiskFormat = func(string, int) error { return nil }
	if err := createUserDisk(64); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(userDiskFlag, []byte(userDiskImage+"\n"), 0644)
	if s := userDiskState(); !s.Enabled || s.Attached {
		t.Fatalf("enabled: %+v", s)
	}
	os.WriteFile(userDiskLun, []byte(userDiskImage+"\n"), 0644)
	if s := userDiskState(); !s.Attached {
		t.Fatalf("attached: %+v", s)
	}
	os.WriteFile(userDiskLun, []byte("/data/other.img\n"), 0644)
	if s := userDiskState(); s.Attached {
		t.Fatalf("another image on the LUN counts as attached: %+v", s)
	}
}

// With a real mkfs.vfat (dosfstools in CI, BusyBox on the device) the image
// is a FAT32 volume that mtools can list.
func TestCreateUserDiskWithRealFormatter(t *testing.T) {
	if _, err := exec.LookPath("mkfs.vfat"); err != nil {
		t.Skip("no mkfs.vfat")
	}
	setupUserDisk(t)
	if err := createUserDisk(64); err != nil {
		t.Fatal(err)
	}
	boot := make([]byte, 90)
	f, err := os.Open(userDiskImage)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.ReadAt(boot, 0); err != nil {
		t.Fatal(err)
	}
	if string(boot[82:87]) != "FAT32" {
		t.Fatalf("type %q", boot[82:90])
	}
	if _, err := exec.LookPath("mdir"); err == nil {
		out, err := exec.Command("mdir", "-i", userDiskImage, "::").CombinedOutput()
		if err != nil || !strings.Contains(string(out), userDiskLabel) {
			t.Fatalf("mdir: %v %s", err, out)
		}
	}
}
