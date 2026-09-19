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
	Style    string `json:"style"`
	Revision string `json:"revision"`
}
type Store struct {
	Directory string
	mu        sync.Mutex
}

func New() *Store              { return &Store{Directory: "/etc/kvm/branding"} }
func validStyle(s string) bool { return s == "connection" || s == "screen" || s == "custom" }
func (s *Store) read() Config {
	c := Config{Style: "connection"}
	b, e := os.ReadFile(filepath.Join(s.Directory, "config.json"))
	if e == nil {
		_ = json.Unmarshal(b, &c)
	}
	if !validStyle(c.Style) {
		c.Style = "connection"
	}
	if c.Style == "custom" {
		if _, e := os.Stat(filepath.Join(s.Directory, "logo.png")); e != nil {
			c.Style = "connection"
		}
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
	_, e := os.Stat(filepath.Join(s.Directory, "logo.png"))
	title, _ := os.ReadFile(filepath.Join(filepath.Dir(s.Directory), "web-title"))
	var rsp proto.Response
	c.Header("Cache-Control", "no-store")
	rsp.OkRspWithData(c, gin.H{"style": config.Style, "revision": config.Revision, "customAvailable": e == nil, "title": strings.TrimSpace(string(title))})
}
func (s *Store) Get(c *gin.Context) { s.mu.Lock(); defer s.mu.Unlock(); s.status(c) }
func (s *Store) Set(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var req struct {
		Style string `json:"style"`
	}
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	if c.ShouldBindJSON(&req) != nil || !validStyle(req.Style) {
		rsp.ErrRsp(c, -1, "Invalid logo choice")
		return
	}
	if req.Style == "custom" {
		if _, e := os.Stat(filepath.Join(s.Directory, "logo.png")); e != nil {
			rsp.ErrRsp(c, -1, "Upload a custom logo first")
			return
		}
	}
	config := s.read()
	config.Style = req.Style
	if e := s.save(config); e != nil {
		rsp.ErrRsp(c, -1, "Could not save branding")
		return
	}
	s.status(c)
}
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
func (s *Store) Upload(c *gin.Context) {
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
	file := filepath.Join(s.Directory, "logo.png")
	if e = os.WriteFile(file+".tmp", data, 0600); e == nil {
		e = os.Rename(file+".tmp", file)
	}
	if e != nil {
		rsp.ErrRsp(c, -1, "Could not save logo")
		return
	}
	sum := sha256.Sum256(data)
	if e = s.save(Config{Style: "custom", Revision: hex.EncodeToString(sum[:])}); e != nil {
		rsp.ErrRsp(c, -1, "Could not save branding")
		return
	}
	s.status(c)
}
func (s *Store) Image(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, e := os.ReadFile(filepath.Join(s.Directory, "logo.png"))
	if e != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "image/png", data)
}
func (s *Store) Delete(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rsp proto.Response
	if e := s.save(Config{Style: "connection"}); e != nil {
		rsp.ErrRsp(c, -1, "Could not reset branding")
		return
	}
	if e := os.Remove(filepath.Join(s.Directory, "logo.png")); e != nil && !os.IsNotExist(e) {
		rsp.ErrRsp(c, -1, "Could not remove custom logo")
		return
	}
	s.status(c)
}
