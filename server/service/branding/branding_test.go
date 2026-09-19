package branding

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"image"
	"image/png"
	"net/http/httptest"
	"testing"
)

func TestBrandingLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Store{Directory: t.TempDir()}
	r := gin.New()
	r.GET("/", s.Get)
	r.POST("/", s.Set)
	r.POST("/logo", s.Upload)
	r.GET("/logo", s.Image)
	r.DELETE("/logo", s.Delete)
	call := func(method, path string, b []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	code := func(w *httptest.ResponseRecorder) int {
		var v struct {
			Code int `json:"code"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		return v.Code
	}
	if s.read().Style != "connection" {
		t.Fatal("default")
	}
	if code(call("POST", "/", []byte(`{"style":"custom"}`))) == 0 {
		t.Fatal("custom without upload")
	}
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 32, 20)))
	if code(call("POST", "/logo", b.Bytes())) != 0 {
		t.Fatal("upload")
	}
	persisted := (&Store{Directory: s.Directory}).read()
	if persisted.Style != "custom" || persisted.Revision == "" {
		t.Fatal("persistence")
	}
	w := call("GET", "/logo", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("image")
	}
	if code(call("POST", "/logo", []byte("<svg></svg>"))) == 0 {
		t.Fatal("invalid image accepted")
	}
	if s.read() != persisted {
		t.Fatal("invalid upload changed config")
	}
	if code(call("POST", "/", []byte(`{"style":"screen"}`))) != 0 || s.read().Style != "screen" {
		t.Fatal("switch")
	}
	if code(call("DELETE", "/logo", nil)) != 0 || s.read().Style != "connection" || call("GET", "/logo", nil).Code != 404 {
		t.Fatal("delete")
	}
}
func TestOversizedDimensions(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1025, 1)))
	if _, e := normalizeLogo(b.Bytes()); e == nil {
		t.Fatal("oversized dimensions accepted")
	}
}
