package middleware

import (
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"NanoKVM-Server/config"

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
// Reads of the API are refused too when the browser marks them cross-site:
// some GET routes act (a Wi-Fi scan, mounting the user disk, reading the
// screen), and an image tag on any page could call them.
func RejectCrossOriginWrites() gin.HandlerFunc {
	return func(c *gin.Context) {
		crossSite := strings.EqualFold(strings.TrimSpace(c.GetHeader("Sec-Fetch-Site")), "cross-site")
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			if crossSite && strings.HasPrefix(c.Request.URL.Path, "/api/") && !allowedCrossSiteRead(c.Request) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": -1, "msg": "cross-origin request refused"})
				return
			}
			c.Next()
			return
		}
		if !CheckWebSocketOrigin(c.Request) || (c.GetHeader("Origin") == "" && crossSite) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": -1, "msg": "cross-origin request refused"})
			return
		}
		c.Next()
	}
}

// allowedCrossSiteRead lets the origins in security.allowedOrigins read
// across sites, as AllowedOriginCORS does. Browsers send Origin with CORS
// reads and preflights.
func allowedCrossSiteRead(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	return origin != "" && AllowedOrigin(origin)
}

// deviceHostname is the device's name; a variable for tests.
var deviceHostname = os.Hostname

// RequireDeviceHost refuses plain-HTTP requests whose Host is not the device:
// an IP address, localhost, the device name or name.local, a host of
// security.allowedOrigins, or anything a trusted reverse proxy forwards.
// Without authentication a page on another site could otherwise rebind its
// DNS name to the device's address: the browser then treats the device as
// that site, and the Origin and Host checks match. Over HTTPS the certificate
// does not match such a name, so the check is not needed there.
func RequireDeviceHost() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.TLS == nil && !deviceHost(c.Request) {
			c.AbortWithStatusJSON(http.StatusMisdirectedRequest, gin.H{"code": -1, "msg": "unknown host name: open the device by its address or add the name to security.allowedOrigins"})
			return
		}
		c.Next()
	}
}

func deviceHost(request *http.Request) bool {
	if isTrustedProxy(request.RemoteAddr, config.GetInstance().Security.TrustedProxies) {
		return true
	}
	host, _ := splitHostPort(request.Host)
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if name, err := deviceHostname(); err == nil && name != "" {
		name = strings.ToLower(name)
		if host == name || host == name+".local" {
			return true
		}
	}
	for _, allowed := range config.GetInstance().Security.AllowedOrigins {
		if parsed, err := url.Parse(strings.TrimSpace(allowed)); err == nil && strings.EqualFold(parsed.Hostname(), host) {
			return true
		}
	}
	return false
}
