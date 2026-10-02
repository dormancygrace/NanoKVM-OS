package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// DefaultBodyLimit caps request bodies on routes that do not size their own.
// JSON and form binding buffer the whole body, so without a cap one request
// can exhaust the device's memory, including the unauthenticated login.
const DefaultBodyLimit int64 = 1 << 20

// LimitRequestBody applies DefaultBodyLimit to every route except the listed
// full paths, which stream their body or install their own MaxBytesReader.
func LimitRequestBody(ownLimit ...string) gin.HandlerFunc {
	exempt := make(map[string]bool, len(ownLimit))
	for _, path := range ownLimit {
		exempt[path] = true
	}
	return func(c *gin.Context) {
		if c.Request.Body != nil && c.Request.Body != http.NoBody && !exempt[c.FullPath()] {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, DefaultBodyLimit)
		}
		c.Next()
	}
}
