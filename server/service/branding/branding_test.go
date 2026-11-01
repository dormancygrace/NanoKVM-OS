package branding

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBrandingLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Store{Directory: filepath.Join(t.TempDir(), "branding")}
	r := gin.New()
	r.GET("/", s.Get)
	r.POST("/logo", s.UploadLogo)
	r.GET("/logo", s.Logo)
	r.DELETE("/logo", s.DeleteLogo)
	r.POST("/favicon", s.UploadFavicon)
	r.GET("/favicon", s.Favicon)
	r.DELETE("/favicon", s.DeleteFavicon)
	r.POST("/button-color", s.SetButtonColor)
	r.DELETE("/button-color", s.DeleteButtonColor)
	r.POST("/banner-style", s.SetBannerStyle)
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
	imageBytes := func(width, height int) []byte {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	buttonColor := func(w *httptest.ResponseRecorder) (string, bool) {
		var response struct {
			Data struct {
				ButtonColor       string `json:"buttonColor"`
				CustomButtonColor bool   `json:"customButtonColor"`
			} `json:"data"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
			t.Fatal(e)
		}
		return response.Data.ButtonColor, response.Data.CustomButtonColor
	}
	bannerStyle := func(w *httptest.ResponseRecorder) string {
		var response struct {
			Data struct {
				BannerStyle string `json:"bannerStyle"`
			} `json:"data"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
			t.Fatal(e)
		}
		return response.Data.BannerStyle
	}
	if color, custom := buttonColor(call("GET", "/", nil)); color != DefaultButtonColor || custom {
		t.Fatalf("default button color = %q, custom = %v", color, custom)
	}
	if color, custom := buttonColor(call("POST", "/button-color", []byte(`{"color":"#123abc"}`))); color != "#123ABC" || !custom {
		t.Fatalf("custom button color = %q, custom = %v", color, custom)
	}
	if code(call("POST", "/button-color", []byte(`{"color":"blue"}`))) == 0 || s.read().ButtonColor != "#123ABC" {
		t.Fatal("invalid button color changed config")
	}
	if color, custom := buttonColor(call("DELETE", "/button-color", nil)); color != DefaultButtonColor || custom || s.read().ButtonColor != "" {
		t.Fatalf("reset button color = %q, custom = %v", color, custom)
	}
	if style := bannerStyle(call("GET", "/", nil)); style != BannerStyleDefault {
		t.Fatalf("default banner style = %q", style)
	}
	if style := bannerStyle(call("POST", "/banner-style", []byte(`{"style":"rainbow"}`))); style != BannerStyleRainbow {
		t.Fatalf("saved banner style = %q", style)
	}
	if data, err := os.ReadFile(s.bannerStylePath()); err != nil || string(data) != BannerStyleRainbow+"\n" {
		t.Fatalf("persisted banner style = %q, %v", data, err)
	}
	if info, err := os.Stat(s.bannerStylePath()); err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("banner style mode = %v, %v", info, err)
	}
	if code(call("POST", "/banner-style", []byte(`{"style":"unknown"}`))) == 0 || s.bannerStyle() != BannerStyleRainbow {
		t.Fatal("invalid banner style changed selection")
	}
	oversized := []byte(`{"style":"` + strings.Repeat("x", 1024) + `"}`)
	if code(call("POST", "/banner-style", oversized)) == 0 || s.bannerStyle() != BannerStyleRainbow {
		t.Fatal("oversized banner style request changed selection")
	}
	if style := bannerStyle(call("POST", "/banner-style", []byte(`{"style":"default"}`))); style != BannerStyleDefault {
		t.Fatalf("restored banner style = %q", style)
	}
	if _, err := os.Stat(s.bannerStylePath()); !os.IsNotExist(err) {
		t.Fatalf("default banner style left an override: %v", err)
	}
	if err := os.WriteFile(s.bannerStylePath(), []byte("unknown\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if style := s.bannerStyle(); style != BannerStyleDefault {
		t.Fatalf("unknown persisted banner style = %q", style)
	}
	if err := os.Remove(s.bannerStylePath()); err != nil {
		t.Fatal(err)
	}
	if code(call("POST", "/logo", imageBytes(32, 20))) != 0 {
		t.Fatal("upload")
	}
	if code(call("POST", "/favicon", imageBytes(24, 24))) != 0 {
		t.Fatal("favicon upload")
	}
	persisted := (&Store{Directory: s.Directory}).read()
	if persisted.LogoRevision == "" || persisted.FaviconRevision == "" || persisted.LogoRevision == persisted.FaviconRevision {
		t.Fatal("persistence")
	}
	w := call("GET", "/logo", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("image")
	}
	etag := w.Header().Get("ETag")
	conditional := httptest.NewRequest(http.MethodGet, "/logo", nil)
	conditional.Header.Set("If-None-Match", etag)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, conditional)
	if etag == "" || w.Code != http.StatusNotModified {
		t.Fatal("conditional image cache")
	}
	if code(call("POST", "/logo", []byte("<svg></svg>"))) == 0 {
		t.Fatal("invalid image accepted")
	}
	if !reflect.DeepEqual(s.read(), persisted) {
		t.Fatal("invalid upload changed config")
	}
	if code(call("DELETE", "/logo", nil)) != 0 || call("GET", "/logo", nil).Code != 404 {
		t.Fatal("logo delete")
	}
	if call("GET", "/favicon", nil).Code != 200 || s.read().FaviconRevision != persisted.FaviconRevision {
		t.Fatal("logo reset changed favicon")
	}
	if code(call("POST", "/logo", imageBytes(40, 22))) != 0 {
		t.Fatal("logo re-upload")
	}
	logoRevision := s.read().LogoRevision
	if code(call("DELETE", "/favicon", nil)) != 0 || call("GET", "/favicon", nil).Code != 404 {
		t.Fatal("favicon delete")
	}
	if call("GET", "/logo", nil).Code != 200 || s.read().LogoRevision != logoRevision {
		t.Fatal("favicon reset changed logo")
	}
}

func TestOversizedDimensions(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1025, 1)))
	if _, e := normalizeLogo(b.Bytes()); e == nil {
		t.Fatal("oversized dimensions accepted")
	}
}
