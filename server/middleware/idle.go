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
// The clocks are kept in memory, per sign-in (sessionKeyOf). A period starts
// when the sign-in was made, or when the server started or the limit was
// changed if that was later: a sign-in that was idle before is given one
// period from then, not counted against a limit nobody had set.

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
	// since: when the server started or the limit was last changed. No
	// period starts before it.
	since time.Time
	// deadBefore: a sign-in made at or before it that has no clock (made no
	// request since the server started) ran out of a limit that has been
	// changed since. A clock from an active policy is retained when the limit
	// is disabled so a sign-in that was still active stays valid.
	deadBefore time.Time
	clocks     map[string]*idleClock
}{clocks: map[string]*idleClock{}, since: time.Now()}

// idleSetting serializes changes of the limit: the file and the limit in
// memory change together, in the order the changes were made. It is not the
// idle lock, which every signed-in request takes, so the card write does not
// hold up requests.
var idleSetting sync.Mutex

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
// that a shorter limit does not end sign-ins on the spot. A sign-in that has
// run out of the limit in force is over first, whether or not a request has
// noticed it yet: changing the limit brings none back.
func SetIdleLimit(seconds int) error {
	if !validIdleLimit(seconds) {
		return errors.New("not a limit that can be set")
	}
	idleSetting.Lock()
	defer idleSetting.Unlock()

	// The limit in force, read before its file is replaced.
	prior := IdleLimit()
	next := time.Duration(seconds) * time.Second
	if prior == next {
		return nil
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
		// Nothing changed in memory either.
		return err
	}

	now := time.Now()
	idle.Lock()
	defer idle.Unlock()
	if prior > 0 {
		// Out of time under the limit in force: those with a clock, and,
		// by deadBefore, those that made no request at all.
		for _, clock := range idle.clocks {
			if now.Sub(clock.last) >= prior {
				clock.over = true
			}
		}
		if dead := now.Add(-prior); !idle.since.After(dead) && dead.After(idle.deadBefore) {
			idle.deadBefore = dead
		}
	}
	idle.limit = next
	idle.since = now
	for key, clock := range idle.clocks {
		switch {
		case !clock.tokenUntil.After(now):
			delete(idle.clocks, key)
		case !clock.over:
			clock.last = now
		}
	}
	return nil
}

// sessionKeyOf names one sign-in. A token carries its own random id (jti);
// one issued before it had one is named by its user, version and issue time.
func sessionKeyOf(token *Token) string {
	if token.ID != "" {
		return token.Username + "\x00" + strconv.FormatUint(token.TokenVersion, 10) + "\x00id:" + token.ID
	}
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
	return idleStateOf(sessionKeyOf(token), tokenIssued(token), tokenUntil(token), touch, time.Now())
}

func tokenIssued(token *Token) time.Time {
	if token.IssuedAt == nil {
		return time.Time{}
	}
	return token.IssuedAt.Time
}

func tokenUntil(token *Token) time.Time {
	if token.ExpiresAt == nil {
		return time.Time{}
	}
	return token.ExpiresAt.Time
}

func idleStateOf(key string, issued, until time.Time, touch bool, now time.Time) (bool, time.Duration) {
	idle.Lock()
	defer idle.Unlock()
	loadIdleLimitLocked()

	clock := idle.clocks[key]
	if clock != nil && clock.over {
		return false, 0
	}
	// For a sign-in without an active-policy clock, use the same issue
	// timestamp here as in the first-use deadline below.
	if clock == nil && !issued.IsZero() && !issued.After(idle.deadBefore) {
		return false, 0
	}
	if idle.limit == 0 {
		return true, 0
	}
	if clock == nil {
		// The first sight of a sign-in: its period started when it was made,
		// or when the server started or the limit changed, if later. Forget
		// the sign-ins whose tokens have expired meanwhile.
		for other, known := range idle.clocks {
			if !known.tokenUntil.After(now) {
				delete(idle.clocks, other)
			}
		}
		start := idle.since
		if issued.After(start) {
			start = issued
		}
		if start.After(now) {
			start = now
		}
		clock = &idleClock{last: start, tokenUntil: until}
		idle.clocks[key] = clock
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

// streamRoutes are the routes, other than websockets, whose answer goes on
// for as long as the page is open: the MJPEG video and the GPIO events.
var streamRoutes = map[string]bool{
	"/api/stream/mjpeg":   true,
	"/api/vm/gpio/events": true,
}

// isStream reports a request that goes on for as long as its page is open,
// by the route it matched. The rest (an upload, a download) ends by itself.
func isStream(c *gin.Context) bool {
	return c.IsWebsocket() || streamRoutes[c.FullPath()]
}
