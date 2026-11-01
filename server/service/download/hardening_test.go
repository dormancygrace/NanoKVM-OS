package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestImageTransferBudgets(t *testing.T) {
	for _, length := range []int{4095, 4096, 4097, 10000} {
		var dst bytes.Buffer
		_, err := copyImageWithin(&dst, bytes.NewReader(make([]byte, length)), nil, 4096)
		if (length > 4096) != errors.Is(err, errImageSpace) {
			t.Fatalf("length=%d err=%v", length, err)
		}
		if dst.Len() > 4096 {
			t.Fatal("budget exceeded")
		}
	}
}

func TestNativeAndGoDownloadsEnforceSpace(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, unknown := range []bool{false, true} {
			for _, size := range []int{4096, 8192} {
				t.Run(fmt.Sprintf("curl=%v/chunked=%v/bytes=%d", native, unknown, size), func(t *testing.T) {
					budgetForTest(t, 4096)
					s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if unknown {
							w.(http.Flusher).Flush()
						} else {
							w.Header().Set("Content-Length", fmt.Sprint(size))
						}
						_, _ = w.Write(bytes.Repeat([]byte{'a'}, size))
					}))
					defer s.Close()
					f := curlDestination(t)
					var err error
					if native {
						err = downloadCurlTransfer(context.Background(), s.URL, f, nil, nil, nil)
					} else {
						err = downloadGoImage(context.Background(), s.URL, f, nil, nil)
					}
					if size > 4096 && !errors.Is(err, errImageSpace) {
						t.Fatalf("oversized download error=%v", err)
					}
					if size <= 4096 && err != nil {
						t.Fatalf("fitting download failed: %v", err)
					}
					st, _ := f.Stat()
					if st.Size() > 4096 {
						t.Fatalf("wrote %d beyond budget", st.Size())
					}
				})
			}
		}
	}
}

func TestResumeBudgetCountsOnlyRemainingBytes(t *testing.T) {
	const offset = 8192
	for _, budget := range []int64{4095, 4096} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			budgetForTest(t, budget)
			var requests atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Length", "4096")
				w.Header().Set("Content-Range", "bytes 8192-12287/12288")
				w.Header().Set("ETag", "\"v1\"")
				w.WriteHeader(206)
				_, _ = w.Write(bytes.Repeat([]byte{'b'}, 4096))
			}))
			defer s.Close()
			f := curlDestination(t)
			_, _ = f.Write(bytes.Repeat([]byte{'a'}, offset))
			err := downloadCurlTransfer(context.Background(), s.URL, f, nil, nil, &imageResume{ETag: "\"v1\"", Total: 12288})
			if budget < 4096 {
				if !errors.Is(err, errImageSpace) || requests.Load() != 0 {
					t.Fatalf("preflight err=%v requests=%d", err, requests.Load())
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func opticalFixture(ids ...string) []byte {
	b := make([]byte, 0x8000+(len(ids)+1)*2048)
	for i, id := range ids {
		copy(b[0x8001+i*2048:], id)
	}
	return b
}

func TestOpticalDetection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"iso", opticalFixture("CD001"), true},
		{"udf2", opticalFixture("BEA01", "NSR02", "TEA01"), true},
		{"udf3", opticalFixture("BEA01", "NSR03", "TEA01"), true},
		{"bridge", opticalFixture("CD001", "BEA01", "NSR03"), true},
		{"bea-only", opticalFixture("BEA01"), false},
		{"unframed", opticalFixture("NSR03"), false},
		{"html", []byte("<html>download failed</html>"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if opticalImage(bytes.NewReader(tc.data)) != tc.valid {
				t.Fatal("wrong optical detection")
			}
		})
	}
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

func TestCleanupKeepsValidatedResumeAndUserFiles(t *testing.T) {
	dir := downloadFixture(t)
	keep := filepath.Join(dir, ".nanokvm-download-123")
	for _, name := range []string{filepath.Base(keep), ".nanokvm-download-456", ".nanokvm-upload-789", ".nanokvm-notes", ".nanokvm-upload-notes", ".nanokvm-upload-4294967296"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(dir, "folder")
	_ = os.Mkdir(nested, 0700)
	_ = os.WriteFile(filepath.Join(nested, ".nanokvm-upload-111"), []byte("user file"), 0600)
	link := filepath.Join(dir, ".nanokvm-upload-222")
	_ = os.Symlink(filepath.Join(dir, ".nanokvm-notes"), link)
	job := &imageResume{URL: "https://example.test/test.iso", Path: keep, Filename: "test.iso", ETag: "\"v1\"", Total: 100}
	if err := saveImageResume(imageResumePath, job); err != nil {
		t.Fatal(err)
	}
	cleanupImageTransfers(dir, imageResumePath)
	for _, name := range []string{".nanokvm-download-456", ".nanokvm-upload-789"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("orphan retained: %s", name)
		}
	}
	for _, name := range []string{filepath.Base(keep), ".nanokvm-notes", ".nanokvm-upload-notes", ".nanokvm-upload-4294967296", "folder/.nanokvm-upload-111", ".nanokvm-upload-222", "resume.json"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("preserved file missing: %s", name)
		}
	}
	// Corrupt metadata is not grounds to retain an orphan forever.
	_ = os.WriteFile(imageResumePath, []byte("invalid"), 0600)
	cleanupImageTransfers(dir, imageResumePath)
	if _, err := os.Stat(keep); !os.IsNotExist(err) {
		t.Fatal("invalid resume partial retained")
	}
}

func TestBudgetFailureBeforeNetwork(t *testing.T) {
	old := imageWriteBudget
	imageWriteBudget = func(*os.File) (int64, error) { return 0, io.ErrUnexpectedEOF }
	t.Cleanup(func() { imageWriteBudget = old })
	f := curlDestination(t)
	err := downloadCurlTransfer(context.Background(), "http://127.0.0.1:1", f, nil, nil, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("did not fail closed: %v", err)
	}
}

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
