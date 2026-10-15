package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

func fileRequest(t *testing.T, handler gin.HandlerFunc, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/?path="+url.QueryEscape(path), body)
	handler(c)
	return rec
}
func fileCode(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var rsp struct{ Code int }
	if err := json.Unmarshal(rec.Body.Bytes(), &rsp); err != nil {
		t.Fatalf("%s: %v", rec.Body.String(), err)
	}
	return rsp.Code
}
func mockUserDiskMount(t *testing.T) string {
	t.Helper()
	dir := setupUserDisk(t)
	files := filepath.Join(dir, "files")
	if err := os.Mkdir(files, 0700); err != nil {
		t.Fatal(err)
	}
	original := mountUserDisk
	mountUserDisk = func(bool) (*os.Root, func() error, error) {
		root, err := os.OpenRoot(files)
		if err != nil {
			return nil, nil, err
		}
		return root, root.Close, nil
	}
	t.Cleanup(func() { mountUserDisk = original })
	return files
}

func TestUserDiskPaths(t *testing.T) {
	for _, value := range []string{"../secret", "foo/../secret", "/etc/passwd", "a//b", "a/./b", "x\\y", "a\x00b", "a:", "file.", "file ", strings.Repeat("a", 256), "\xff"} {
		if _, err := userDiskPath(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, value := range []string{"", ".", "folder/BIOS 1.bin", "Папка/配置.txt"} {
		if _, err := userDiskPath(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
}
func TestUserDiskFilesRoundTripAndNoOverwrite(t *testing.T) {
	files := mockUserDiskMount(t)
	s := &Service{}
	if code := fileCode(t, fileRequest(t, s.MakeUserDiskDirectory, "BIOS", nil)); code != 0 {
		t.Fatalf("mkdir %d", code)
	}
	content := []byte("firmware\x00binary")
	if code := fileCode(t, fileRequest(t, s.UploadUserDiskFile, "BIOS/update.bin", bytes.NewReader(content))); code != 0 {
		t.Fatalf("upload %d", code)
	}
	duplicate := fileRequest(t, s.UploadUserDiskFile, "BIOS/update.bin", strings.NewReader("replacement"))
	if fileCode(t, duplicate) == 0 || !strings.Contains(duplicate.Body.String(), "exists") {
		t.Fatal(duplicate.Body.String())
	}
	download := fileRequest(t, s.DownloadUserDiskFile, "BIOS/update.bin", nil)
	if !bytes.Equal(download.Body.Bytes(), content) || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %v %q", download.Header(), download.Body.Bytes())
	}
	listing := fileRequest(t, s.ListUserDiskFiles, "BIOS", nil)
	if fileCode(t, listing) != 0 || !strings.Contains(listing.Body.String(), "update.bin") {
		t.Fatal(listing.Body.String())
	}
	if fileCode(t, fileRequest(t, s.DeleteUserDiskFile, "BIOS", nil)) == 0 {
		t.Fatal("removed nonempty folder")
	}
	if fileCode(t, fileRequest(t, s.DeleteUserDiskFile, ".", nil)) == 0 {
		t.Fatal("removed root")
	}
	for _, name := range []string{"BIOS/update.bin", "BIOS"} {
		if code := fileCode(t, fileRequest(t, s.DeleteUserDiskFile, name, nil)); code != 0 {
			t.Fatalf("delete %s: %d", name, code)
		}
	}
	entries, _ := os.ReadDir(files)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}
func TestUserDiskFilesRefuseHostAndUnreadableState(t *testing.T) {
	mockUserDiskMount(t)
	s := &Service{}
	handlers := []gin.HandlerFunc{s.ListUserDiskFiles, s.UploadUserDiskFile, s.DownloadUserDiskFile, s.MakeUserDiskDirectory, s.DeleteUserDiskFile}
	for _, marker := range []string{userDiskFlag, userDiskLun} {
		if err := os.WriteFile(marker, []byte(userDiskImage+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, h := range handlers {
			if code := fileCode(t, fileRequest(t, h, "file", strings.NewReader("data"))); code != -2 {
				t.Fatalf("host %s code %d", marker, code)
			}
		}
		os.Remove(marker)
	}
	if err := os.Mkdir(userDiskFlag, 0700); err != nil {
		t.Fatal(err)
	}
	if fileCode(t, fileRequest(t, s.ListUserDiskFiles, ".", nil)) == 0 {
		t.Fatal("unreadable marker treated as off")
	}
}
func TestUserDiskRootCannotEscape(t *testing.T) {
	files := mockUserDiskMount(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600)
	if err := os.Symlink(outside, filepath.Join(files, "escape")); err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	for _, h := range []gin.HandlerFunc{s.ListUserDiskFiles, s.DownloadUserDiskFile, s.UploadUserDiskFile, s.MakeUserDiskDirectory, s.DeleteUserDiskFile} {
		if fileCode(t, fileRequest(t, h, "escape/secret", strings.NewReader("replace"))) == 0 {
			t.Fatal("symlink escaped root")
		}
	}
	value, _ := os.ReadFile(filepath.Join(outside, "secret"))
	if string(value) != "secret" {
		t.Fatal("outside file changed")
	}
}

type brokenUpload struct{ read bool }

func (b *brokenUpload) Read(p []byte) (int, error) {
	if !b.read {
		b.read = true
		return copy(p, "partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}
func TestUserDiskUploadFailureRemovesPartialFile(t *testing.T) {
	files := mockUserDiskMount(t)
	s := &Service{}
	for _, body := range []io.Reader{&brokenUpload{}, strings.NewReader("short")} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/?path=file", body)
		c.Request.ContentLength = 100
		s.UploadUserDiskFile(c)
		if fileCode(t, rec) == 0 {
			t.Fatal("incomplete upload succeeded")
		}
		entries, _ := os.ReadDir(files)
		if len(entries) != 0 {
			t.Fatalf("left partial files: %v", entries)
		}
	}
}
func TestUserDiskTransferHoldsCompositionLockThroughCleanup(t *testing.T) {
	files := mockUserDiskMount(t)
	started, resume := make(chan struct{}), make(chan struct{})
	mountUserDisk = func(bool) (*os.Root, func() error, error) {
		root, err := os.OpenRoot(files)
		return root, func() error {
			if unlock, e := LockUserDiskForComposition(); e == nil {
				unlock()
				t.Error("composition acquired before unmount")
			}
			close(started)
			<-resume
			return root.Close()
		}, err
	}
	done := make(chan *httptest.ResponseRecorder)
	go func() { done <- fileRequest(t, (&Service{}).UploadUserDiskFile, "file", strings.NewReader("data")) }()
	<-started
	if unlock, err := LockUserDiskForComposition(); !errors.Is(err, ErrUserDiskBusy) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("composition during cleanup: %v", err)
	}
	if code := fileCode(t, fileRequest(t, (&Service{}).ListUserDiskFiles, ".", nil)); code != -6 {
		t.Fatalf("parallel read: %d", code)
	}
	close(resume)
	if fileCode(t, <-done) != 0 {
		t.Fatal("upload failed")
	}
	unlock, err := LockUserDiskForComposition()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

// Opt-in: real loop + VFAT integration, using only a new temporary 64 MiB
// image on the build host. Run as root, never point it at a device/SD card.
func TestUserDiskRealMount(t *testing.T) {
	if os.Getenv("NANOKVM_TEST_USER_DISK_MOUNT") != "1" {
		t.Skip("set NANOKVM_TEST_USER_DISK_MOUNT=1 as root on a Linux build host")
	}
	dir := setupUserDisk(t)
	oldPoint := userDiskMountpoint
	userDiskMountpoint = filepath.Join(dir, "mount point")
	t.Cleanup(func() { unix.Unmount(userDiskMountpoint, 0); userDiskMountpoint = oldPoint })
	if err := createUserDisk(64); err != nil {
		t.Fatal(err)
	}
	root, cleanup, err := mountUserDiskImage(true)
	if err != nil {
		t.Fatal(err)
	}
	if err = userDiskUnmounted(); !errors.Is(err, ErrUserDiskBusy) {
		t.Fatal("mounted disk not excluded", err)
	}
	if unlock, err := LockUserDiskForComposition(); !errors.Is(err, ErrUserDiskBusy) {
		if unlock != nil {
			unlock()
		}
		t.Fatal("composition accepted a mounted disk", err)
	}
	free, total, err := userDiskSpace(root)
	if err != nil || free >= total {
		t.Fatalf("FAT space: %d/%d %v", free, total, err)
	}
	// Must compare available bytes, not total size.
	if err = uploadUserDisk(root, "too-large", strings.NewReader(""), int64(free+1)); !errors.Is(err, errUserDiskSpace) {
		t.Fatal("accepted beyond free space", err)
	}
	if err = uploadUserDisk(root, "hello.txt", strings.NewReader("hello"), 5); err != nil {
		t.Fatal(err)
	}
	// VFAT case-insensitive collision must preserve the original.
	if err = uploadUserDisk(root, "HELLO.TXT", strings.NewReader("bad"), 3); !errors.Is(err, os.ErrExist) {
		t.Fatal("case collision", err)
	}
	if err = uploadUserDisk(root, "прошивка-配置.bin", strings.NewReader("unicode"), 7); err != nil {
		t.Fatal(err)
	}
	// Unknown-length input must stop at the real free-space bound and remove
	// the incomplete upload, without allocating that much RAM.
	free, _, err = userDiskSpace(root)
	if err != nil {
		t.Fatal(err)
	}
	err = uploadUserDisk(root, "fills-disk.bin", io.LimitReader(zeroUpload{}, int64(free)+1), -1)
	if !errors.Is(err, errUserDiskSpace) && !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("unknown-size full disk: %v", err)
	}
	if _, err = root.Stat("fills-disk.bin"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file survived: %v", err)
	}
	if err = cleanup(); err != nil {
		t.Fatal(err)
	}
	if err = userDiskUnmounted(); err != nil {
		t.Fatal(err)
	}
	root, cleanup, err = mountUserDiskImage(false)
	if err != nil {
		t.Fatal(err)
	}
	unicodeData, err := root.ReadFile("прошивка-配置.bin")
	if err != nil || string(unicodeData) != "unicode" {
		t.Fatalf("UTF-8 round trip: %q %v", unicodeData, err)
	}
	data, err := root.ReadFile("hello.txt")
	if err != nil || string(data) != "hello" {
		t.Fatalf("readback %q %v", data, err)
	}
	if f, err := root.Create("readonly"); err == nil {
		f.Close()
		t.Fatal("read mount writable")
	}
	if err = cleanup(); err != nil {
		t.Fatal(err)
	}
	// Simulate a process leaving its private mount behind: the next operation
	// retires it, and still sees the complete file.
	root, _, err = mountUserDiskImage(false)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	root, cleanup, err = mountUserDiskImage(true)
	if err != nil {
		t.Fatal("stale mount recovery:", err)
	}
	if data, err = root.ReadFile("hello.txt"); err != nil || string(data) != "hello" {
		t.Fatalf("recovery %q %v", data, err)
	}
	if err = cleanup(); err != nil {
		t.Fatal(err)
	}
	// Failed vfat mount must not leak a loop-device association.
	if err = os.WriteFile(userDiskImage, make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	if _, close, err := mountUserDiskImage(false); err == nil {
		close()
		t.Fatal("invalid FAT image mounted")
	}
	// Loop AUTOCLEAR releases asynchronously if udev briefly opens the device.
	deadline := time.Now().Add(3 * time.Second)
	for {
		leaked := ""
		loops, _ := filepath.Glob("/sys/block/loop*/loop/backing_file")
		for _, file := range loops {
			data, _ := os.ReadFile(file)
			if strings.TrimSpace(string(data)) == userDiskImage {
				leaked = file
				break
			}
		}
		if leaked == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leaked loop: %s", leaked)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUserDiskDirectoryPagination(t *testing.T) {
	files := mockUserDiskMount(t)
	for n := 0; n < userDiskPageSize+3; n++ {
		if err := os.WriteFile(filepath.Join(files, fmt.Sprintf("file-%03d", n)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(files)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first, err := listUserDisk(root, ".", 0)
	if err != nil || len(first.Entries) != userDiskPageSize || !first.Next {
		t.Fatalf("first page: %+v %v", first, err)
	}
	last, err := listUserDisk(root, ".", userDiskPageSize)
	if err != nil || len(last.Entries) != 3 || last.Next {
		t.Fatalf("last page: %+v %v", last, err)
	}
	seen := map[string]bool{}
	for _, entry := range append(first.Entries, last.Entries...) {
		if seen[entry.Name] {
			t.Fatalf("repeated %s", entry.Name)
		}
		seen[entry.Name] = true
	}
}

type zeroUpload struct{}

func (zeroUpload) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestFileErrorDistinguishesNonEmptyDirectoryFromCollision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		reason string
	}{
		{"nonempty directory", &os.PathError{Op: "remove", Path: "folder", Err: unix.ENOTEMPTY}, "notEmpty"},
		{"existing file", &os.PathError{Op: "open", Path: "file", Err: unix.EEXIST}, "exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			fileError(c, errors.Join(tc.err, nil))
			var rsp struct {
				Code int
				Msg  string
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &rsp); err != nil {
				t.Fatal(err)
			}
			if rsp.Code != -3 || rsp.Msg != tc.reason {
				t.Fatalf("response: %s", rec.Body.String())
			}
		})
	}
}
