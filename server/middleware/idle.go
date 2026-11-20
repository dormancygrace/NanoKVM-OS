package middleware

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/internal/atomicfile"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// Sign-out after inactivity. A sign-in lasts for weeks, so a browser left
// open stays a way in. With a limit set, a sign-in that has shown no activity
// for that long is over: its requests are refused, its websockets are closed,
// and it stays over whatever comes later.
//
// Activity is what the client reports (ReportActivity): input in the page,
// not its requests, which a page makes by itself for as long as it is open.
// A request with an Authorization header has no page behind it, and counts
// as activity itself.
//
// The clocks are kept in memory, per sign-in (sessionKeyOf). After a restart
// of the server every sign-in starts a new period.

// idleLimitFile holds the limit in seconds; no file is no limit.
var idleLimitFile = "/etc/kvm/idle-signout"

// IdleLimits are the limits that can be set, in seconds; 0 is none.
var IdleLimits = []int{0, 300, 900, 1800, 3600, 14400}

type idleClock struct {
	last time.Time
	// over: the sign-in ran out of time. It is kept until the token expires
	// so that no later report or change of the limit brings it back.
	over       bool
	tokenUntil time.Time
}

var idle = struct {
	sync.Mutex
	loaded bool
	limit  time.Duration
	clocks map[string]*idleClock
}{clocks: map[string]*idleClock{}}

// loadIdleLimitLocked reads the limit once. idle is locked.
func loadIdleLimitLocked() {
	if idle.loaded {
		return
	}
	idle.loaded = true
	data, err := os.ReadFile(idleLimitFile)
	if err != nil {
		return
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || !validIdleLimit(seconds) {
		log.Warnf("ignoring %s: not a limit that can be set", idleLimitFile)
		return
	}
	idle.limit = time.Duration(seconds) * time.Second
}

func validIdleLimit(seconds int) bool {
	for _, allowed := range IdleLimits {
		if seconds == allowed {
			return true
		}
	}
	return false
}

// IdleLimit is the limit in force; 0 is none.
func IdleLimit() time.Duration {
	idle.Lock()
	defer idle.Unlock()
	loadIdleLimitLocked()
	return idle.limit
}

// SetIdleLimit stores the limit and starts every running period again, so
// that a shorter limit does not end sign-ins on the spot.
func SetIdleLimit(seconds int) error {
	if !validIdleLimit(seconds) {
		return errors.New("not a limit that can be set")
	}
	var err error
	if seconds == 0 {
		if err = os.Remove(idleLimitFile); os.IsNotExist(err) {
			err = nil
		}
	} else {
		err = atomicfile.Write(idleLimitFile, []byte(strconv.Itoa(seconds)+"\n"), 0o644)
	}
	if err != nil {
		return err
	}

	now := time.Now()
	idle.Lock()
	defer idle.Unlock()
	idle.loaded = true
	idle.limit = time.Duration(seconds) * time.Second
	for key, clock := range idle.clocks {
		switch {
		case !clock.tokenUntil.After(now):
			delete(idle.clocks, key)
		case clock.over:
		case seconds == 0:
			delete(idle.clocks, key)
		default:
			clock.last = now
		}
	}
	return nil
}

func sessionKeyOf(token *Token) string {
	issued := int64(0)
	if token.IssuedAt != nil {
		issued = token.IssuedAt.UnixNano()
	}
	return token.Username + "\x00" + strconv.FormatUint(token.TokenVersion, 10) + "\x00" + strconv.FormatInt(issued, 10)
}

// idleState reports whether the sign-in is still on and how much of its
// period is left (0 with no limit). touch starts the period again.
func idleState(token *Token, touch bool) (alive bool, left time.Duration) {
	if token == nil {
		return true, 0
	}
	return idleStateOf(sessionKeyOf(token), tokenUntil(token), touch, time.Now())
}

func tokenUntil(token *Token) time.Time {
	if token.ExpiresAt == nil {
		return time.Time{}
	}
	return token.ExpiresAt.Time
}

func idleStateOf(key string, until time.Time, touch bool, now time.Time) (bool, time.Duration) {
	idle.Lock()
	defer idle.Unlock()
	loadIdleLimitLocked()

	clock := idle.clocks[key]
	if clock != nil && clock.over {
		return false, 0
	}
	if idle.limit == 0 {
		return true, 0
	}
	if clock == nil {
		// The first sight of a sign-in: a new one, or the first since the
		// server started. Forget those whose tokens have expired meanwhile.
		for other, known := range idle.clocks {
			if !known.tokenUntil.After(now) {
				delete(idle.clocks, other)
			}
		}
		idle.clocks[key] = &idleClock{last: now, tokenUntil: until}
		return true, idle.limit
	}
	left := idle.limit - now.Sub(clock.last)
	if left <= 0 {
		clock.over = true
		return false, 0
	}
	if touch {
		clock.last = now
		left = idle.limit
	}
	return true, left
}

// IdleStatus is the limit and what is left of the request's period, both in
// seconds. touch reports activity first.
func IdleStatus(c *gin.Context, touch bool) (limit, left int) {
	seconds := int(IdleLimit() / time.Second)
	value, ok := c.Get(tokenContextKey)
	token, isToken := value.(*Token)
	if !ok || !isToken || token == nil || seconds == 0 {
		return seconds, 0
	}
	_, remaining := idleState(token, touch)
	// Rounded up: the page asks again when the time is out, and must not be
	// told so before it is.
	return seconds, int((remaining + time.Second - 1) / time.Second)
}

// ClearSessionCookie removes the sign-in from the browser.
func ClearSessionCookie(c *gin.Context) {
	secure := SessionCookieName(c.Request) == CookieName
	c.SetSameSite(http.SameSiteStrictMode)
	// Clear the legacy cookie too, so HTTP fallback cannot restore a session
	// after logout when token revocation is disabled. HTTPS can clear both;
	// HTTP cannot remove a Secure cookie left by a previous HTTPS session.
	for _, name := range []string{CookieName, HTTPCookieName} {
		c.SetCookie(name, "", -1, "/", "", secure, true)
	}
}

// isStream reports a request that goes on for as long as its page is open: a
// websocket, the MJPEG video, server-sent events. The rest (an upload, a
// download) ends by itself.
func isStream(c *gin.Context) bool {
	return c.IsWebsocket() || c.Request.URL.Path == "/api/stream/mjpeg" ||
		strings.Contains(c.GetHeader("Accept"), "text/event-stream")
}
