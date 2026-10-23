package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	cors "github.com/rs/cors/wrapper/gin"
)

// AllowedOriginCORS answers cross-origin reads and preflights for the
// origins listed in security.allowedOrigins only, such as a development
// web server (web/README.md). It replaces allowing every origin: without
// authentication that let any page the user opened read the API.
func AllowedOriginCORS() gin.HandlerFunc {
	return cors.New(cors.Options{
		AllowOriginFunc: AllowedOrigin,
		AllowedMethods: []string{
			http.MethodHead, http.MethodGet, http.MethodPost,
			http.MethodPut, http.MethodPatch, http.MethodDelete,
		},
		AllowedHeaders: []string{"*"},
	})
}

// AllowedOrigin reports whether origin is listed in security.allowedOrigins.
func AllowedOrigin(origin string) bool {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	return err == nil && validOriginURL(parsed) && isAllowedOrigin(parsed)
}

// RejectCrossOriginWrites refuses state-changing requests that a browser
// sends from another site. Without authentication every request acts as the
// administrator, so any web page the user opens could otherwise post to the
// device (scripts, power, updates). The rule is the WebSocket one: the
// device's own origin and security.allowedOrigins pass. Requests without an
// Origin header (curl, automation) pass, unless the browser marks them
// cross-site.
func RejectCrossOriginWrites() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		crossSite := strings.EqualFold(strings.TrimSpace(c.GetHeader("Sec-Fetch-Site")), "cross-site")
		if !CheckWebSocketOrigin(c.Request) || (c.GetHeader("Origin") == "" && crossSite) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": -1, "msg": "cross-origin request refused"})
			return
		}
		c.Next()
	}
}
