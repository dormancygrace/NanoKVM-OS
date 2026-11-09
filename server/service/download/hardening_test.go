package download

import (
	"bytes"

	"encoding/json"

	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"testing"

	"github.com/gin-gonic/gin"
)

func budgetForTest(t *testing.T, n int64) {
	t.Helper()
	old := imageWriteBudget
	imageWriteBudget = func(*os.File) (int64, error) { return n, nil }
	t.Cleanup(func() { imageWriteBudget = old })
}

func downloadFixture(t *testing.T) string {
	t.Helper()
	oldDir, oldResume, oldSentinel := imageDirectory, imageResumePath, transferSentinelPath
	dir := t.TempDir()
	imageDirectory, imageResumePath, transferSentinelPath = dir, filepath.Join(dir, "resume.json"), filepath.Join(dir, "transfer.lock")
	t.Cleanup(func() { imageDirectory, imageResumePath, transferSentinelPath = oldDir, oldResume, oldSentinel })
	return dir
}

func opticalFixture(ids ...string) []byte {
	b := make([]byte, 0x8000+(len(ids)+1)*2048)
	for i, id := range ids {
		copy(b[0x8001+i*2048:], id)
	}
	return b
}

func TestBrowserImageUploadFormatsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   []byte
		budget int64
		ok     bool
	}{
		{"installer.iso", opticalFixture("CD001"), 1 << 20, true},
		{"pure-udf.ISO", opticalFixture("BEA01", "NSR03"), 1 << 20, true},
		{"disk.IMG", make([]byte, 1024), 1 << 20, true},
		{"invalid.iso", []byte("not an iso"), 1 << 20, false},
		{"partial.img", make([]byte, 513), 1 << 20, false},
		{"oversized.img", make([]byte, 8192), 4096, false},
		{"file.txt", make([]byte, 1024), 1 << 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := downloadFixture(t)
			budgetForTest(t, tc.budget)
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			part, err := mw.CreateFormFile("file", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write(tc.data)
			_ = mw.Close()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", &body)
			c.Request.Header.Set("Content-Type", mw.FormDataContentType())
			(&Service{}).DownloadImageFile(c)
			var rsp struct{ Code int }
			if err := json.Unmarshal(rec.Body.Bytes(), &rsp); err != nil {
				t.Fatal(err)
			}
			if (rsp.Code == 0) != tc.ok {
				t.Fatalf("response %s", rec.Body)
			}
			path := filepath.Join(dir, tc.name)
			if tc.ok {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, tc.data) {
					t.Fatalf("saved bytes differ: %v", err)
				}
			} else if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("invalid image published")
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if imageTransferTemp(entry.Name()) {
					t.Fatal("partial upload retained")
				}
			}
		})
	}
}

// Corrupt metadata is not grounds to retain an orphan forever.

func TestExactTransferNames(t *testing.T) {
	for _, name := range []string{".nanokvm-upload-1", ".nanokvm-download-4294967295"} {
		if !imageTransferTemp(name) {
			t.Fatal(name)
		}
	}
	for _, name := range strings.Fields(".nanokvm-notes .nanokvm-upload- .nanokvm-upload-foo .nanokvm-upload-123.iso .nanokvm-upload-4294967296") {
		if imageTransferTemp(name) {
			t.Fatal(name)
		}
	}
}
