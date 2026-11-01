package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
