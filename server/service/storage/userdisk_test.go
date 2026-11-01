package storage

import (
	"os"
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
