package branding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

type Config struct {
	// Style and Revision are retained only to read installations created by
	// older releases. Built-in branding now always uses the connection style.
	Style           string `json:"style,omitempty"`
	Revision        string `json:"revision,omitempty"`
	LogoRevision    string `json:"logoRevision,omitempty"`
	FaviconRevision string `json:"faviconRevision,omitempty"`
	LogoCustom      *bool  `json:"logoCustom,omitempty"`
	FaviconCustom   *bool  `json:"faviconCustom,omitempty"`
	ButtonColor     string `json:"buttonColor,omitempty"`
}
type Store struct {
	Directory string
	mu        sync.Mutex
}

const (
	logoFile           = "logo.png"
	faviconFile        = "favicon.png"
	bannerStyleFile    = "banner-style"
	DefaultButtonColor = "#45E9A0"
	BannerStyleDefault = "default"
	BannerStyleRainbow = "rainbow"
)

func New() *Store { return &Store{Directory: "/etc/kvm/branding"} }
func (s *Store) read() Config {
	var c Config
	b, e := os.ReadFile(filepath.Join(s.Directory, "config.json"))
	if e == nil {
		_ = json.Unmarshal(b, &c)
	}
	// A legacy custom logo used Revision. Preserve it as the main login logo;
	// favicon customization starts empty and therefore uses the built-in icon.
	if c.LogoCustom == nil {
		active := c.Style == "custom"
		c.LogoCustom = &active
		if active && c.LogoRevision == "" {
			c.LogoRevision = c.Revision
		}
	}
	if c.FaviconCustom == nil {
		active := c.FaviconRevision != ""
		c.FaviconCustom = &active
	}
	return c
}
func (s *Store) save(c Config) error {
	if e := os.MkdirAll(s.Directory, 0700); e != nil {
		return e
	}
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	file := filepath.Join(s.Directory, "config.json")
	if e = os.WriteFile(file+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(file+".tmp", file)
}
func (s *Store) status(c *gin.Context) {
	config := s.read()
	logoRevision, logoAvailable := s.assetStatus(logoFile, config.LogoRevision, *config.LogoCustom)
	faviconRevision, faviconAvailable := s.assetStatus(faviconFile, config.FaviconRevision, *config.FaviconCustom)
	buttonColor, customButtonColor := effectiveButtonColor(config.ButtonColor)
	bannerStyle := s.bannerStyle()
	title, _ := os.ReadFile(filepath.Join(filepath.Dir(s.Directory), "web-title"))
	legacyStyle := "connection"
	if logoAvailable {
		legacyStyle = "custom"
	}
	var rsp proto.Response
	c.Header("Cache-Control", "no-store")
	rsp.OkRspWithData(c, gin.H{
		"logoRevision":           logoRevision,
		"faviconRevision":        faviconRevision,
		"customLogoAvailable":    logoAvailable,
		"customFaviconAvailable": faviconAvailable,
		"buttonColor":            buttonColor,
		"customButtonColor":      customButtonColor,
		"bannerStyle":            bannerStyle,
		"title":                  strings.TrimSpace(string(title)),
		// Compatibility fields keep an older frontend usable during an
		// update without reintroducing the removed style selector.
		"style":           legacyStyle,
		"revision":        logoRevision,
		"customAvailable": logoAvailable,
	})
}
func (s *Store) Get(c *gin.Context) { s.mu.Lock(); defer s.mu.Unlock(); s.status(c) }
func normalizeLogo(data []byte) ([]byte, error) {
	cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 1024 || cfg.Height > 1024 {
		return nil, &logoSizeError{}
	}
	img, _, e := image.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	var output bytes.Buffer
	e = png.Encode(&output, img)
	return output.Bytes(), e
}

type logoSizeError struct{}

func (*logoSizeError) Error() string { return "Logo dimensions must not exceed 1024 x 1024" }
func (s *Store) assetStatus(filename, configuredRevision string, enabled bool) (string, bool) {
	if !enabled {
		return "", false
	}
	data, e := os.ReadFile(filepath.Join(s.Directory, filename))
	if e != nil {
		return "", false
	}
	if configuredRevision != "" {
		return configuredRevision, true
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

func (s *Store) upload(c *gin.Context, filename string, favicon bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*1024*1024)
	raw, e := io.ReadAll(c.Request.Body)
	if e != nil {
		rsp.ErrRsp(c, -1, "Logo must be at most 2 MiB")
		return
	}
	data, e := normalizeLogo(raw)
	if e != nil {
		rsp.ErrRsp(c, -1, "Use a PNG or JPEG image up to 1024 x 1024")
		return
	}
	if e = os.MkdirAll(s.Directory, 0700); e != nil {
		rsp.ErrRsp(c, -1, "Could not save logo")
		return
	}
	file := filepath.Join(s.Directory, filename)
	if e = os.WriteFile(file+".tmp", data, 0600); e == nil {
		e = os.Rename(file+".tmp", file)
	}
	if e != nil {
		rsp.ErrRsp(c, -1, "Could not save logo")
		return
	}
	sum := sha256.Sum256(data)
	config := s.read()
	if favicon {
		config.FaviconRevision = hex.EncodeToString(sum[:])
		enabled := true
		config.FaviconCustom = &enabled
	} else {
		config.LogoRevision = hex.EncodeToString(sum[:])
		enabled := true
		config.LogoCustom = &enabled
	}
	if e = s.save(config); e != nil {
		rsp.ErrRsp(c, -1, "Could not save branding")
		return
	}
	s.status(c)
}

func (s *Store) UploadLogo(c *gin.Context)    { s.upload(c, logoFile, false) }
func (s *Store) UploadFavicon(c *gin.Context) { s.upload(c, faviconFile, true) }

func (s *Store) image(c *gin.Context, filename string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, e := os.ReadFile(filepath.Join(s.Directory, filename))
	if e != nil {
		c.Status(http.StatusNotFound)
		return
	}
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	c.Header("Cache-Control", "no-cache")
	c.Header("ETag", etag)
	c.Header("X-Content-Type-Options", "nosniff")
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "image/png", data)
}

func (s *Store) Logo(c *gin.Context)    { s.image(c, logoFile) }
func (s *Store) Favicon(c *gin.Context) { s.image(c, faviconFile) }

func (s *Store) delete(c *gin.Context, filename string, favicon bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	if e := os.Remove(filepath.Join(s.Directory, filename)); e != nil && !os.IsNotExist(e) {
		rsp.ErrRsp(c, -1, "Could not remove custom image")
		return
	}
	config := s.read()
	if favicon {
		config.FaviconRevision = ""
		enabled := false
		config.FaviconCustom = &enabled
	} else {
		config.LogoRevision = ""
		config.Revision = ""
		enabled := false
		config.LogoCustom = &enabled
	}
	if e := s.save(config); e != nil {
		rsp.ErrRsp(c, -1, "Could not reset branding")
		return
	}
	s.status(c)
}

func (s *Store) DeleteLogo(c *gin.Context)    { s.delete(c, logoFile, false) }
func (s *Store) DeleteFavicon(c *gin.Context) { s.delete(c, faviconFile, true) }

func normalizeButtonColor(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 7 || value[0] != '#' {
		return "", false
	}
	for _, char := range value[1:] {
		if !((char >= '0' && char <= '9') || (char >= 'A' && char <= 'F')) {
			return "", false
		}
	}
	return value, true
}

func effectiveButtonColor(value string) (string, bool) {
	if color, ok := normalizeButtonColor(value); ok {
		return color, true
	}
	return DefaultButtonColor, false
}

func (s *Store) SetButtonColor(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	var request struct {
		Color string `json:"color"`
	}
	if e := c.ShouldBindJSON(&request); e != nil {
		rsp.ErrRsp(c, -1, "Use a six-digit hex color")
		return
	}
	color, ok := normalizeButtonColor(request.Color)
	if !ok {
		rsp.ErrRsp(c, -1, "Use a six-digit hex color")
		return
	}
	config := s.read()
	config.ButtonColor = color
	if e := s.save(config); e != nil {
		rsp.ErrRsp(c, -1, "Could not save button color")
		return
	}
	s.status(c)
}

func (s *Store) DeleteButtonColor(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	config := s.read()
	config.ButtonColor = ""
	if e := s.save(config); e != nil {
		rsp.ErrRsp(c, -1, "Could not reset button color")
		return
	}
	s.status(c)
}

func normalizeBannerStyle(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case BannerStyleDefault:
		return BannerStyleDefault, true
	case BannerStyleRainbow:
		return BannerStyleRainbow, true
	default:
		return "", false
	}
}

func (s *Store) bannerStylePath() string {
	return filepath.Join(filepath.Dir(s.Directory), bannerStyleFile)
}

func (s *Store) bannerStyle() string {
	data, err := os.ReadFile(s.bannerStylePath())
	if err != nil {
		return BannerStyleDefault
	}
	style, ok := normalizeBannerStyle(string(data))
	if !ok {
		return BannerStyleDefault
	}
	return style
}

func (s *Store) saveBannerStyle(style string) error {
	path := s.bannerStylePath()
	if style == BannerStyleDefault {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(style+"\n"), 0644); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (s *Store) SetBannerStyle(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var request struct {
		Style string `json:"style"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		rsp.ErrRsp(c, -1, "Choose the default or rainbow banner")
		return
	}
	style, ok := normalizeBannerStyle(request.Style)
	if !ok {
		rsp.ErrRsp(c, -1, "Choose the default or rainbow banner")
		return
	}
	if err := s.saveBannerStyle(style); err != nil {
		rsp.ErrRsp(c, -1, "Could not save banner style")
		return
	}
	s.status(c)
}
