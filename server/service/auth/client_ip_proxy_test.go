package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/config"
)

// Behind a trusted reverse proxy (tailscale serve connects from loopback)
// every request has the proxy's address; the lockout key must be the
// forwarded client, or one guesser locks every user out.
func TestLockoutKeyBehindATrustedProxy(t *testing.T) {
	conf := config.GetInstance()
	original := conf.Security.TrustedProxies
	conf.Security.TrustedProxies = []string{"127.0.0.1/32", "::1/128"}
	defer func() { conf.Security.TrustedProxies = original }()

	key := func(remote, forwarded string) string {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/api/auth/login", nil)
		c.Request.RemoteAddr = remote
		if forwarded != "" {
			c.Request.Header.Set("X-Forwarded-For", forwarded)
		}
		return GetClientIP(c)
	}
	attacker, owner := key("127.0.0.1:5000", "100.64.0.66"), key("127.0.0.1:5001", "100.64.0.7")
	if attacker == owner {
		t.Fatalf("two clients of the proxy share the key %q", attacker)
	}
	// A direct client cannot choose its key with the header.
	if got := key("192.0.2.9:5000", "100.64.0.7"); got != "192.0.2.9" {
		t.Fatalf("direct client keyed as %q", got)
	}
	// IPv6 clients behind the proxy are still grouped by /64.
	if a, b := key("[::1]:5000", "2001:db8:1:2::1"), key("[::1]:5000", "2001:db8:1:2::2"); a != b {
		t.Fatalf("one /64 behind the proxy has two keys: %q %q", a, b)
	}
}
