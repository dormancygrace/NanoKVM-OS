package router

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/contrib/static"
	"github.com/gin-gonic/gin"
)

func TestAssetsServePrecompressedHashedFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0755)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0644)
	os.WriteFile(filepath.Join(dir, "assets", "app-abc.js"), []byte("plain"), 0644)
	os.WriteFile(filepath.Join(dir, "assets", "small-abc.js"), []byte("small"), 0644)
	gz, _ := os.Create(filepath.Join(dir, "assets", "app-abc.js.gz"))
	w := gzip.NewWriter(gz)
	w.Write([]byte("compressed"))
	w.Close()
	gz.Close()

	r := gin.New()
	r.Use(assets(dir))
	r.Use(static.Serve("/", static.LocalFile(dir, true)))
	get := func(path, encoding string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if encoding != "" {
			req.Header.Set("Accept-Encoding", encoding)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := get("/assets/app-abc.js", "br, gzip;q=0.8")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip: %d %v", rec.Code, rec.Header())
	}
	if kind := rec.Header().Get("Content-Type"); kind != "text/javascript; charset=utf-8" {
		t.Fatalf("content type %q", kind)
	}
	body, _ := gzip.NewReader(rec.Body)
	if data, _ := io.ReadAll(body); string(data) != "compressed" {
		t.Fatalf("body %q", data)
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("cache %q", rec.Header().Get("Cache-Control"))
	}

	for _, encoding := range []string{"", "identity", "gzip;q=0"} {
		rec = get("/assets/app-abc.js", encoding)
		if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "plain" {
			t.Fatalf("%q: %v %q", encoding, rec.Header(), rec.Body.String())
		}
	}
	// No .gz: the plain file, still cacheable.
	rec = get("/assets/small-abc.js", "gzip")
	if rec.Body.String() != "small" || rec.Header().Get("Cache-Control") == "" {
		t.Fatalf("small: %v %q", rec.Header(), rec.Body.String())
	}
	// A missing hashed asset (e.g. mid-update) is an uncached 404, even when
	// a stray .gz exists.
	os.WriteFile(filepath.Join(dir, "assets", "orphan-abc.js.gz"), []byte("x"), 0644)
	for _, missing := range []string{"/assets/not-yet-installed-abc.js", "/assets/orphan-abc.js"} {
		rec = get(missing, "gzip")
		if rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "" ||
			rec.Header().Get("Content-Encoding") != "" {
			t.Fatalf("%s: %d %v", missing, rec.Code, rec.Header())
		}
	}
	// Outside /assets nothing changes, and paths cannot climb out of it.
	if rec = get("/index.html", "gzip"); rec.Header().Get("Cache-Control") != "" {
		t.Fatalf("index: %v", rec.Header())
	}
	if rec = get("/assets/../index.html", "gzip"); rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("traversal: %v", rec.Header())
	}
}
