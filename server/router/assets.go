package router

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Content-hashed assets may be cached for a year. Compression happens during
// the web build; serving Brotli or gzip adds no compressor to the device.
func assets(webPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := path.Clean("/" + c.Request.URL.Path)
		if !strings.HasPrefix(name, "/assets/") ||
			(c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
			c.Next()
			return
		}
		plain := filepath.Join(webPath, filepath.FromSlash(name))
		if info, err := os.Stat(plain); err != nil || info.IsDir() {
			// Never cache a missing file or serve an orphan compressed variant.
			c.Next()
			return
		}
		quality := encodingQualities(strings.Join(c.Request.Header.Values("Accept-Encoding"), ","))
		file, coding, best, size := plain, "", 0, int64(0)
		if q, explicit := quality["identity"]; explicit {
			best = q
		}
		// Prefer Brotli when qualities are equal, but honour explicit weights.
		for _, candidate := range []struct{ coding, suffix string }{{"br", ".br"}, {"gzip", ".gz"}} {
			q, explicit := quality[candidate.coding]
			if !explicit {
				q = quality["*"]
			}
			if q <= 0 || q < best || (q == best && coding != "") {
				continue
			}
			if info, err := os.Stat(plain + candidate.suffix); err == nil && !info.IsDir() {
				file, coding, best, size = plain+candidate.suffix, candidate.coding, q, info.Size()
			}
		}
		c.Writer.Header().Add("Vary", "Accept-Encoding")
		identity, explicit := quality["identity"]
		wildcard, hasWildcard := quality["*"]
		if coding == "" && ((explicit && identity == 0) || (!explicit && hasWildcard && wildcard == 0)) {
			c.AbortWithStatus(http.StatusNotAcceptable)
			return
		}
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		if coding == "" {
			c.Next()
			return
		}
		if kind := mime.TypeByExtension(path.Ext(name)); kind != "" {
			c.Header("Content-Type", kind)
		}
		c.Header("Content-Encoding", coding)
		// ServeContent otherwise omits the length when Content-Encoding is set,
		// assuming a middleware might be compressing the bytes on the fly.
		c.Header("Content-Length", strconv.FormatInt(size, 10))
		http.ServeFile(c.Writer, c.Request, file)
		c.Abort()
	}
}

// HTTP qvalues have at most three fractional digits. Invalid weights are
// treated as unacceptable rather than accidentally enabling a forbidden codec.
func encodingQualities(header string) map[string]int {
	values := make(map[string]int)
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		coding := strings.ToLower(strings.TrimSpace(parts[0]))
		if coding == "" {
			continue
		}
		q, weighted := 1000, false
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(parameter, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") || weighted {
				q = 0
				break
			}
			q, weighted = encodingQuality(strings.TrimSpace(value)), true
		}
		// A repeated prohibition must not be overridden by another field line.
		if previous, exists := values[coding]; !exists || q < previous {
			values[coding] = q
		}
	}
	return values
}

func encodingQuality(value string) int {
	whole, fraction, _ := strings.Cut(value, ".")
	if (whole != "0" && whole != "1") || len(fraction) > 3 {
		return 0
	}
	q, scale := 0, 100
	for _, digit := range fraction {
		if digit < '0' || digit > '9' || (whole == "1" && digit != '0') {
			return 0
		}
		q += int(digit-'0') * scale
		scale /= 10
	}
	if whole == "1" {
		return 1000
	}
	return q
}
