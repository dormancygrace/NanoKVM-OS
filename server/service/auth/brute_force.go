package auth

import (
	"net/netip"
	"sync"
	"time"

	"NanoKVM-Server/config"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type loginAttempt struct {
	failures   int
	lastFailed time.Time
	lockoutEnd time.Time
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
	loginMutex    sync.Mutex
	cleanupOnce   sync.Once
)

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
		}
	} else if len(loginAttempts) >= maxLoginAttemptsRecords && !hasReclaimableRecordLocked(time.Now()) {
		// Preserve every active lockout. A new address cannot be checked safely
		// while there is no capacity to record its failures.
		return true, -5, loginSaturationMessage
	}

	return false, 0, ""
}

// RecordLoginFailure records a failed login attempt for the given IP address.
func RecordLoginFailure(clientIP string) (bool, int, string) {
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

// ClearLoginAttempt clears the failed login attempt record for an IP upon successful login.
func ClearLoginAttempt(clientIP string) {
	conf := config.GetInstance()
	if conf.Security.LoginLockoutDuration <= 0 {
		return
	}

	loginMutex.Lock()
	defer loginMutex.Unlock()

	delete(loginAttempts, clientIP)
}
