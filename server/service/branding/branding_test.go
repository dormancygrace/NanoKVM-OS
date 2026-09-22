package branding

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"
	"NanoKVM-Server/middleware"
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

func TestLegacyConfigPreservesCustomLogoAndIgnoresStyle(t *testing.T) {
	for _, style := range []string{"connection", "screen", "custom", "unknown"} {
		t.Run(style, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "config.json"), []byte(`{"style":"`+style+`","revision":"legacy-revision"}`), 0600); err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, logoFile), b.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			s := &Store{Directory: directory}
			config := s.read()
			if style == "custom" && config.LogoRevision != "legacy-revision" {
				t.Fatalf("legacy revision not migrated for style %q", style)
			}
			if style != "custom" && config.LogoRevision != "" {
				t.Fatalf("inactive legacy logo enabled for style %q", style)
			}
			r := gin.New()
			r.GET("/", s.Get)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			var response struct {
				Data struct {
					Style                  string `json:"style"`
					CustomLogoAvailable    bool   `json:"customLogoAvailable"`
					CustomFaviconAvailable bool   `json:"customFaviconAvailable"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if style == "custom" && (response.Data.Style != "custom" || !response.Data.CustomLogoAvailable || response.Data.CustomFaviconAvailable) {
				t.Fatalf("unexpected active legacy status: %+v", response.Data)
			}
			if style != "custom" && (response.Data.Style != "connection" || response.Data.CustomLogoAvailable || response.Data.CustomFaviconAvailable) {
				t.Fatalf("unexpected inactive legacy status: %+v", response.Data)
			}
			if _, err := os.Stat(filepath.Join(directory, logoFile)); err != nil {
				t.Fatalf("legacy logo removed for style %q: %v", style, err)
			}
		})
	}
}

func TestBrandingWritesRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf := config.GetInstance()
	originalAuthentication := conf.Authentication
	conf.Authentication = "enable"
	t.Cleanup(func() { conf.Authentication = originalAuthentication })

	s := &Store{Directory: filepath.Join(t.TempDir(), "branding")}
	r := gin.New()
	r.GET("/api/branding", s.Get)
	r.GET("/api/branding/logo", s.Logo)
	r.GET("/api/branding/favicon", s.Favicon)
	admin := r.Group("/api/branding").Use(
		middleware.CheckToken(),
		middleware.RequireRole(authn.RoleAdmin),
	)
	admin.POST("/logo", s.UploadLogo)
	admin.DELETE("/logo", s.DeleteLogo)
	admin.POST("/favicon", s.UploadFavicon)
	admin.DELETE("/favicon", s.DeleteFavicon)
	admin.POST("/button-color", s.SetButtonColor)
	admin.DELETE("/button-color", s.DeleteButtonColor)
	admin.POST("/banner-style", s.SetBannerStyle)

	public := httptest.NewRecorder()
	r.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/branding", nil))
	if public.Code != http.StatusOK {
		t.Fatalf("public branding status = %d", public.Code)
	}
	for _, path := range []string{"/api/branding/logo", "/api/branding/favicon"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("public GET %s = %d, want 404 for missing image", path, w.Code)
		}
	}

	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/branding/logo"},
		{http.MethodDelete, "/api/branding/logo"},
		{http.MethodPost, "/api/branding/favicon"},
		{http.MethodDelete, "/api/branding/favicon"},
		{http.MethodPost, "/api/branding/button-color"},
		{http.MethodDelete, "/api/branding/button-color"},
		{http.MethodPost, "/api/branding/banner-style"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(request.method, request.path, strings.NewReader("image")))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", request.method, request.path, w.Code)
		}
	}
}

func TestOversizedDimensions(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1025, 1)))
	if _, e := normalizeLogo(b.Bytes()); e == nil {
		t.Fatal("oversized dimensions accepted")
	}
}
