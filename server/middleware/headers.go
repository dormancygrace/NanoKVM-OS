package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets the headers every response carries: no page of the
// device may be framed (the interface frames none of its own), the browser
// keeps to the declared content types, and no address of the device leaks as
// a referrer to the sites its pages link to.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}
