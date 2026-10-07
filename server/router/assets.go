package router

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// assets serves the content-hashed build files under /assets: they never
// change under the same name, so browsers may keep them for a year, and the
// precompressed .gz written at build time (web/scripts/precompress.mjs) is
// sent to clients that accept gzip. Everything else falls through to the
// static file server.
func assets(webPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := path.Clean("/" + c.Request.URL.Path)
		if !strings.HasPrefix(name, "/assets/") {
			c.Next()
			return
		}
		// Only an asset that exists may be cached: a 404 during a package
		// replacement must not stick in browsers once the file arrives.
		plain := filepath.Join(webPath, filepath.FromSlash(name))
		if info, err := os.Stat(plain); err != nil || info.IsDir() {
			c.Next()
			return
		}
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Header("Vary", "Accept-Encoding")
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead ||
			!acceptsGzip(c.GetHeader("Accept-Encoding")) {
			c.Next()
			return
		}
		file := plain + ".gz"
		if info, err := os.Stat(file); err != nil || info.IsDir() {
			c.Next()
			return
		}
		// Set before serving, or the type would come from the .gz name.
		if kind := mime.TypeByExtension(path.Ext(name)); kind != "" {
			c.Header("Content-Type", kind)
		}
		c.Header("Content-Encoding", "gzip")
		http.ServeFile(c.Writer, c.Request, file)
		c.Abort()
	}
}

func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.ToLower(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}
