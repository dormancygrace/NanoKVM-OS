package storage

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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

// The review's race: with the disk off, a re-format in one tab and turning
// the disk on in another. The composition must not attach while the image
// is being replaced, and a re-format must not run while it is attached.
func TestCompositionLockExcludesAConcurrentFormat(t *testing.T) {
	setupUserDisk(t)
	userDiskFormat = func(string, int) error { return nil }
	if err := createUserDisk(64); err != nil {
		t.Fatal(err)
	}
	formatting := make(chan struct{})
	release := make(chan struct{})
	userDiskFormat = func(string, int) error {
		close(formatting)
		<-release
		return nil
	}
	gin.SetMode(gin.TestMode)
	s := &Service{}
	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"sizeMiB":128}`))
		c.Request.Header.Set("Content-Type", "application/json")
		s.CreateUserDisk(c)
		var rsp struct{ Code int }
		_ = json.Unmarshal(rec.Body.Bytes(), &rsp)
		done <- rsp.Code
	}()
	<-formatting
	if _, err := LockUserDiskForComposition(); !errors.Is(err, ErrUserDiskBusy) {
		t.Fatalf("composition lock taken during a format: %v", err)
	}
	close(release)
	if code := <-done; code != 0 {
		t.Fatalf("create returned %d", code)
	}
	unlock, err := LockUserDiskForComposition()
	if err != nil {
		t.Fatalf("composition lock after the format: %v", err)
	}

	// The composition holds the lock and turns the disk on; a re-format
	// started meanwhile is refused at once as busy, and after the apply as
	// enabled, the image untouched both times.
	before, _ := os.Stat(userDiskImage)
	userDiskFormat = func(string, int) error {
		t.Error("formatted while the composition was attaching the disk")
		return nil
	}
	create := func() int {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"sizeMiB":64}`))
		c.Request.Header.Set("Content-Type", "application/json")
		s.CreateUserDisk(c)
		var rsp struct{ Code int }
		_ = json.Unmarshal(rec.Body.Bytes(), &rsp)
		return rsp.Code
	}
	if code := create(); code != -6 {
		t.Fatalf("create during the composition returned %d, want -6", code)
	}
	if err := os.WriteFile(userDiskFlag, []byte(userDiskImage+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock()
	if code := create(); code != -2 {
		t.Fatalf("create while enabled returned %d, want -2", code)
	}
	after, _ := os.Stat(userDiskImage)
	if !os.SameFile(before, after) {
		t.Fatal("the attached image was replaced")
	}
}
