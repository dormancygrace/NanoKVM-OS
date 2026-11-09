package storage

import (
	"path/filepath"

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
