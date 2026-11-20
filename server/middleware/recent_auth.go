package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RecentAuthRequiredCode is returned (HTTP 403) by a route that acts as root
// (a shell, scripts, scheduled commands, accounts, SSH, the TLS certificate)
// when the session has not proved its password, and second factor if it has
// one, within RecentAuthWindow: a session lives for weeks, and a stolen or
// left-open one must not be enough for these.
const RecentAuthRequiredCode = -11

// RecentAuthWindow is how long a proof lasts.
const RecentAuthWindow = 5 * time.Minute

var recentAuth = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// sessionKey names the session of the request: its account and the token's
// version and issue time, which differ for every login.
func sessionKey(c *gin.Context) (string, bool) {
	value, ok := c.Get(tokenContextKey)
	if !ok {
		return "", false
	}
	token, ok := value.(*Token)
	if !ok || token == nil {
		return "", false
	}
	return sessionKeyOf(token), true
}

// MarkRecentAuth records that the session has just proved its credentials.
func MarkRecentAuth(c *gin.Context) {
	key, ok := sessionKey(c)
	if !ok {
		return
	}
	now := time.Now()
	recentAuth.Lock()
	defer recentAuth.Unlock()
	for k, at := range recentAuth.at {
		if now.Sub(at) >= RecentAuthWindow {
			delete(recentAuth.at, k)
		}
	}
	recentAuth.at[key] = now
}

// RecentAuth reports whether the session proved its credentials within
// RecentAuthWindow. Without authentication (a test setup) there is nothing
// to prove.
func RecentAuth(c *gin.Context) bool {
	if _, ok := c.Get(tokenContextKey); !ok {
		return false
	}
	key, ok := sessionKey(c)
	if !ok {
		value, _ := c.Get(tokenContextKey)
		token, _ := value.(*Token)
		return token == nil // authentication disabled
	}
	recentAuth.Lock()
	defer recentAuth.Unlock()
	at, found := recentAuth.at[key]
	return found && time.Since(at) < RecentAuthWindow
}

// RequireRecentAuth refuses the route unless the session proved its
// credentials within RecentAuthWindow (POST /api/auth/recheck).
func RequireRecentAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !RecentAuth(c) {
			c.JSON(http.StatusForbidden, gin.H{"code": RecentAuthRequiredCode, "msg": "confirm your password to continue"})
			c.Abort()
			return
		}
		c.Next()
	}
}
