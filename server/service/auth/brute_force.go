package auth

import (
	"net/netip"
	"sync"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type loginAttempt struct {
	failures   int
	lastFailed time.Time
	lockoutEnd time.Time
	// failedUser is the one user name the failures were for; otherFailures
	// is set once they were for more than one name, or for none that could
	// be read. Only then may a successful login keep the record.
	failedUser    string
	otherFailures bool
}

const (
	maxLoginAttemptsRecords = 3000
	cleanupInterval         = 6 * time.Hour

	// staleRecordWindow is how long a record with no lockout on it is kept
	// after its last failure.
	staleRecordWindow      = 30 * time.Minute
	loginSaturationMessage = "Too many failed login attempts, please try again later"
)

var (
	loginAttempts = make(map[string]*loginAttempt)
	// loginsInFlight holds the addresses with a password check running. One
	// check at a time per address: parallel guesses would otherwise all pass
	// CheckLoginAttempt before the first failure is recorded.
	loginsInFlight = make(map[string]struct{})
	loginMutex     sync.Mutex
	loginSlotFreed = sync.NewCond(&loginMutex)
	cleanupOnce    sync.Once
)

// loginSlotWait bounds how long an attempt waits for the one before it from
// the same address (a double-click, or a queue of guesses).
var loginSlotWait = 10 * time.Second

const loginInFlightMessage = "Another login attempt is in progress, please try again"

// BeginLoginAttempt admits one password check at a time for clientIP. A
// second attempt from the same address waits for the first to finish, then
// sees its recorded failure, so queued guesses still meet the lockout. It
// returns false with an error code and message when the address is locked
// out or the wait timed out; otherwise the caller must call the returned
// release once the attempt, including its failure delay, is over.
func BeginLoginAttempt(clientIP string) (func(), bool, int, string) {
	deadline := time.Now().Add(loginSlotWait)
	wake := time.AfterFunc(loginSlotWait, func() {
		loginMutex.Lock()
		loginSlotFreed.Broadcast()
		loginMutex.Unlock()
	})
	defer wake.Stop()

	loginMutex.Lock()
	for {
		if _, busy := loginsInFlight[clientIP]; !busy {
			break
		}
		if !time.Now().Before(deadline) {
			loginMutex.Unlock()
			return nil, false, -5, loginInFlightMessage
		}
		loginSlotFreed.Wait()
	}
	loginsInFlight[clientIP] = struct{}{}
	loginMutex.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			loginMutex.Lock()
			delete(loginsInFlight, clientIP)
			loginSlotFreed.Broadcast()
			loginMutex.Unlock()
		})
	}
	if locked, code, msg := CheckLoginAttempt(clientIP); locked {
		release()
		return nil, false, code, msg
	}
	return release, true, 0, ""
}

// startCleanupRoutine starts a background routine to clean up memory
func startCleanupRoutine() {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(cleanupInterval)
		for range ticker.C {
			loginMutex.Lock()
			now := time.Now()
			for ip, attempt := range loginAttempts {
				if isExpiredLocked(attempt, now) {
					delete(loginAttempts, ip)
				}
			}
			loginMutex.Unlock()
		}
	}()
}

// GetClientIP returns the brute-force key for a request: the peer address,
// with IPv6 peers grouped by their /64 so one host cannot rotate addresses.
func GetClientIP(c *gin.Context) string {
	ip := c.RemoteIP()
	if ip == "" {
		ip = c.ClientIP()
	}
	return loginAttemptKey(ip)
}

func loginAttemptKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is6() || addr.Is4In6() {
		return ip
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return ip
	}
	return prefix.String()
}

// CheckLoginAttempt checks if a login attempt is allowed based on brute-force protection rules.
// Returning true means the IP/System is locked out, and an error string and error code are returned.
func CheckLoginAttempt(clientIP string) (bool, int, string) {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return false, 0, ""
	}

	cleanupOnce.Do(startCleanupRoutine)

	loginMutex.Lock()
	defer loginMutex.Unlock()

	if attempt, exists := loginAttempts[clientIP]; exists {
		if time.Now().Before(attempt.lockoutEnd) {
			log.Debugf("login blocked for IP %s: account locked due to too many failed attempts (until %s)", clientIP, attempt.lockoutEnd)
			return true, -5, "Account locked due to too many failed attempts, please try again later"
		}

		// If lockout has elapsed, then we reset the failures and lockoutEnd.
		if !attempt.lockoutEnd.IsZero() {
			attempt.failures = 0
			attempt.lockoutEnd = time.Time{}
			attempt.failedUser, attempt.otherFailures = "", false
		}
	} else if len(loginAttempts) >= maxLoginAttemptsRecords && !hasReclaimableRecordLocked(time.Now()) {
		// Preserve every active lockout. A new address cannot be checked safely
		// while there is no capacity to record its failures.
		return true, -5, loginSaturationMessage
	}

	return false, 0, ""
}

// RecordLoginFailure records a failed login attempt for the given IP address
// whose user name is unknown.
func RecordLoginFailure(clientIP string) (bool, int, string) {
	return RecordLoginFailureFor(clientIP, "")
}

// RecordLoginFailureFor records a failed login attempt of username ("" when
// unknown) from the given IP address.
func RecordLoginFailureFor(clientIP, username string) (bool, int, string) {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return false, 0, ""
	}

	cleanupOnce.Do(startCleanupRoutine)

	loginMutex.Lock()
	defer loginMutex.Unlock()

	now := time.Now()
	attempt, exists := loginAttempts[clientIP]
	if !exists {
		// Emptying the table here would hand every locked-out address a clean
		// slate, so an attacker with a range to spend could stay ahead of the
		// limit forever by flushing it. Reclaim one record instead, and pick
		// the one worth the least.
		if len(loginAttempts) >= maxLoginAttemptsRecords && !evictOneRecordLocked() {
			// Saturation can arise after CheckLoginAttempt ran; reject the
			// failure here too instead of allowing untracked retries.
			return true, -5, loginSaturationMessage
		}

		attempt = &loginAttempt{}
		loginAttempts[clientIP] = attempt
	}

	// Failure time window: if it has been a long time since the last failure
	// (e.g., beyond the lockoutDuration window), reset the failure count
	if !attempt.lastFailed.IsZero() && now.Sub(attempt.lastFailed) > time.Duration(conf.Security.LoginLockoutDuration)*time.Second {
		attempt.failures = 0
		attempt.failedUser, attempt.otherFailures = "", false
	}

	// The login request accepts unknown names too. Retain only bounded,
	// valid account names; arbitrary request strings must not live in the
	// address table after the request finishes. Invalid names still count.
	if authn.ValidateUsername(username) != nil {
		username = ""
	}
	switch {
	case username == "":
		attempt.otherFailures = true
	case attempt.failures == 0:
		attempt.failedUser = username
	case username != attempt.failedUser:
		attempt.otherFailures = true
	}
	attempt.failures++
	attempt.lastFailed = now

	// Reach the failure limit, lock out
	if attempt.failures >= conf.Security.LoginMaxFailures {
		attempt.lockoutEnd = now.Add(time.Duration(conf.Security.LoginLockoutDuration) * time.Second)
		log.Debugf("login failures reached threshold for IP %s, locking out until %s", clientIP, attempt.lockoutEnd)
	}

	return false, 0, ""
}

// hasReclaimableRecordLocked checks admission without changing records. An
// unlocked record or an expired lockout can make room for a failed new login.
// The caller must hold loginMutex.
func hasReclaimableRecordLocked(now time.Time) bool {
	for _, attempt := range loginAttempts {
		if !now.Before(attempt.lockoutEnd) {
			return true
		}
	}
	return false
}

// evictOneRecordLocked frees capacity by removing expired records, then the
// oldest unlocked record. Active lockouts are never discarded to admit a new
// address. The caller must hold loginMutex.
func evictOneRecordLocked() bool {
	now := time.Now()
	var oldestUnlockedIP string
	var oldestUnlocked time.Time

	// Reclaim every expired record in one pass so following insertions can
	// reuse the slots without repeating the scan.
	reclaimed := false
	for ip, attempt := range loginAttempts {
		if isExpiredLocked(attempt, now) {
			delete(loginAttempts, ip)
			reclaimed = true
			continue
		}
		if attempt.lockoutEnd.IsZero() && (oldestUnlockedIP == "" || attempt.lastFailed.Before(oldestUnlocked)) {
			oldestUnlockedIP = ip
			oldestUnlocked = attempt.lastFailed
		}
	}
	if reclaimed {
		return true
	}
	if oldestUnlockedIP != "" {
		delete(loginAttempts, oldestUnlockedIP)
		return true
	}
	return false
}

// isExpiredLocked reports whether a record has outlived its usefulness: either
// its lockout has run out, or it never had one and has gone quiet.
func isExpiredLocked(attempt *loginAttempt, now time.Time) bool {
	if !attempt.lockoutEnd.IsZero() {
		return !now.Before(attempt.lockoutEnd)
	}

	return now.Sub(attempt.lastFailed) > staleRecordWindow
}

// ClearLoginAttempt clears the failed login attempt record of an IP after a
// successful login of username, but only if every failure was for that
// user. Otherwise a user who can log in from the same address could reset
// the count between guesses at another account's password.
func ClearLoginAttempt(clientIP, username string) {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return
	}

	loginMutex.Lock()
	defer loginMutex.Unlock()

	if attempt, exists := loginAttempts[clientIP]; exists && (attempt.otherFailures || (attempt.failures > 0 && attempt.failedUser != username)) {
		return
	}
	delete(loginAttempts, clientIP)
}
