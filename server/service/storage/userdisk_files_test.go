package storage

import (
	"encoding/json"

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
