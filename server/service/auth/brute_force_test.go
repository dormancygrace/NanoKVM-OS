package auth

import (
	"fmt"
	"testing"
	"time"

	"NanoKVM-Server/config"
)

func useLoginAttemptRecords(t *testing.T, records map[string]*loginAttempt) {
	t.Helper()
	loginMutex.Lock()
	previous := loginAttempts
	loginAttempts = records
	loginMutex.Unlock()
	t.Cleanup(func() {
		loginMutex.Lock()
		loginAttempts = previous
		loginMutex.Unlock()
	})
}

func TestLoginAttemptExpiration(t *testing.T) {
	now := time.Unix(100000, 0)
	for _, tt := range []struct {
		name    string
		attempt loginAttempt
		expired bool
	}{
		{"active lockout with old failure", loginAttempt{lastFailed: now.Add(-time.Hour), lockoutEnd: now.Add(time.Minute)}, false},
		{"lockout boundary", loginAttempt{lockoutEnd: now}, true},
		{"elapsed lockout", loginAttempt{lockoutEnd: now.Add(-time.Second)}, true},
		{"recent failure", loginAttempt{lastFailed: now.Add(-time.Minute)}, false},
		{"stale failure", loginAttempt{lastFailed: now.Add(-staleRecordWindow - time.Second)}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExpiredLocked(&tt.attempt, now); got != tt.expired {
				t.Fatalf("expired = %v, want %v", got, tt.expired)
			}
		})
	}
}

func TestLoginAttemptEvictionReclaimsAllExpiredRecordsFirst(t *testing.T) {
	now := time.Now()
	locked := &loginAttempt{failures: 5, lastFailed: now.Add(-time.Minute), lockoutEnd: now.Add(time.Hour)}
	unlocked := &loginAttempt{failures: 2, lastFailed: now.Add(-time.Minute)}
	useLoginAttemptRecords(t, map[string]*loginAttempt{
		"locked":          locked,
		"unlocked":        unlocked,
		"expired-lockout": {lockoutEnd: now.Add(-time.Minute)},
		"stale":           {lastFailed: now.Add(-time.Hour)},
	})
	loginMutex.Lock()
	defer loginMutex.Unlock()
	if !evictOneRecordLocked() {
		t.Fatal("expired records were not reclaimed")
	}
	if len(loginAttempts) != 2 || loginAttempts["locked"] != locked || loginAttempts["unlocked"] != unlocked {
		t.Fatalf("live records changed during expiry sweep: %+v", loginAttempts)
	}
}

func TestLoginAttemptEvictionPreservesActiveLockouts(t *testing.T) {
	now := time.Now()
	locked := &loginAttempt{failures: 5, lastFailed: now.Add(-time.Minute), lockoutEnd: now.Add(time.Hour)}
	useLoginAttemptRecords(t, map[string]*loginAttempt{
		"locked": locked,
		"recent": {failures: 2, lastFailed: now},
		"oldest": {failures: 3, lastFailed: now.Add(-time.Minute)},
	})
	loginMutex.Lock()
	defer loginMutex.Unlock()
	if !evictOneRecordLocked() {
		t.Fatal("unlocked record was not evicted")
	}
	if loginAttempts["oldest"] != nil || len(loginAttempts) != 2 || loginAttempts["locked"] != locked {
		t.Fatalf("wrong record evicted: %+v", loginAttempts)
	}
}

func TestRecordLoginFailureAtCapacityDoesNotResetOtherLockouts(t *testing.T) {
	useLoginProtectionConfig(t)
	now := time.Now()
	records := make(map[string]*loginAttempt, maxLoginAttemptsRecords)
	locked := &loginAttempt{failures: 5, lastFailed: now, lockoutEnd: now.Add(time.Hour)}
	records["192.0.2.1"] = locked
	for index := 1; index < maxLoginAttemptsRecords; index++ {
		records[fmt.Sprintf("unlocked-%d", index)] = &loginAttempt{failures: 2, lastFailed: now.Add(-time.Minute)}
	}
	useLoginAttemptRecords(t, records)
	RecordLoginFailure("192.0.2.2")
	if blocked, _, _ := CheckLoginAttempt("192.0.2.1"); !blocked {
		t.Fatal("existing lockout was lost at capacity")
	}
	loginMutex.Lock()
	defer loginMutex.Unlock()
	if len(loginAttempts) != maxLoginAttemptsRecords || loginAttempts["192.0.2.1"] != locked {
		t.Fatalf("capacity or lockout changed: size=%d", len(loginAttempts))
	}
	if inserted := loginAttempts["192.0.2.2"]; inserted == nil || inserted.failures != 1 {
		t.Fatalf("new failure was not tracked: %+v", inserted)
	}
}

func useLoginProtectionConfig(t *testing.T) {
	t.Helper()
	conf := config.GetInstance()
	previousDuration, previousFailures := conf.Security.LoginLockoutDuration, conf.Security.LoginMaxFailures
	conf.Security.LoginLockoutDuration, conf.Security.LoginMaxFailures = 3600, 5
	t.Cleanup(func() {
		conf.Security.LoginLockoutDuration, conf.Security.LoginMaxFailures = previousDuration, previousFailures
	})
}

func fullActiveLockoutRecords(now time.Time) map[string]*loginAttempt {
	records := make(map[string]*loginAttempt, maxLoginAttemptsRecords)
	for index := 0; index < maxLoginAttemptsRecords; index++ {
		records[fmt.Sprintf("locked-%d", index)] = &loginAttempt{
			failures: 5, lastFailed: now, lockoutEnd: now.Add(time.Hour),
		}
	}
	return records
}

func TestLoginAttemptSaturationDeniesNewAddressesAndPreservesAllLockouts(t *testing.T) {
	useLoginProtectionConfig(t)
	now := time.Now()
	records := fullActiveLockoutRecords(now)
	useLoginAttemptRecords(t, records)
	for _, ip := range []string{"192.0.2.10", "192.0.2.11", "192.0.2.12"} {
		if blocked, code, _ := CheckLoginAttempt(ip); !blocked || code != -5 {
			t.Fatalf("new IP check = blocked:%v code:%d, want temporary denial", blocked, code)
		}
		// Also covers saturation between the initial check and failure write.
		if blocked, code, _ := RecordLoginFailure(ip); !blocked || code != -5 {
			t.Fatalf("new IP failure = blocked:%v code:%d, want temporary denial", blocked, code)
		}
	}
	loginMutex.Lock()
	defer loginMutex.Unlock()
	if len(loginAttempts) != maxLoginAttemptsRecords {
		t.Fatalf("saturated table size = %d", len(loginAttempts))
	}
	for index := 0; index < maxLoginAttemptsRecords; index++ {
		attempt := loginAttempts[fmt.Sprintf("locked-%d", index)]
		if attempt == nil || attempt.failures != 5 || !attempt.lastFailed.Equal(now) || !attempt.lockoutEnd.Equal(now.Add(time.Hour)) {
			t.Fatalf("active lockout %d was removed or changed", index)
		}
	}
}

func TestLoginAttemptSaturationExpiresAndAdmitsNewAddress(t *testing.T) {
	useLoginProtectionConfig(t)
	now := time.Now()
	records := fullActiveLockoutRecords(now)
	useLoginAttemptRecords(t, records)
	if blocked, _, _ := CheckLoginAttempt("192.0.2.10"); !blocked {
		t.Fatal("new address accepted before capacity became available")
	}
	loginMutex.Lock()
	records["locked-0"].lockoutEnd = now.Add(-time.Second)
	loginMutex.Unlock()
	if blocked, _, _ := CheckLoginAttempt("192.0.2.10"); blocked {
		t.Fatal("expired lockout did not restore admission")
	}
	if blocked, _, _ := RecordLoginFailure("192.0.2.10"); blocked {
		t.Fatal("expired capacity could not record a new failure")
	}
	loginMutex.Lock()
	defer loginMutex.Unlock()
	if len(loginAttempts) != maxLoginAttemptsRecords || loginAttempts["locked-0"] != nil {
		t.Fatal("expired record was not replaced within the capacity bound")
	}
	if inserted := loginAttempts["192.0.2.10"]; inserted == nil || inserted.failures != 1 {
		t.Fatal("new failure not tracked after expiration")
	}
	for index := 1; index < maxLoginAttemptsRecords; index++ {
		if attempt := loginAttempts[fmt.Sprintf("locked-%d", index)]; attempt == nil || !attempt.lockoutEnd.Equal(now.Add(time.Hour)) {
			t.Fatalf("unexpired lockout %d changed", index)
		}
	}
}

func TestLoginAttemptAtCapacityKeepsTrackedUnlockedAddressUsable(t *testing.T) {
	useLoginProtectionConfig(t)
	now := time.Now()
	records := fullActiveLockoutRecords(now)
	unlocked := &loginAttempt{failures: 2, lastFailed: now, failedUser: "owner"}
	records["locked-0"] = unlocked
	useLoginAttemptRecords(t, records)
	if blocked, _, _ := CheckLoginAttempt("locked-0"); blocked {
		t.Fatal("tracked unlocked address was denied at capacity")
	}
	if blocked, _, _ := RecordLoginFailureFor("locked-0", "owner"); blocked {
		t.Fatal("tracked unlocked address could not record a failure")
	}
	loginMutex.Lock()
	if len(loginAttempts) != maxLoginAttemptsRecords || loginAttempts["locked-0"] != unlocked || unlocked.failures != 3 {
		loginMutex.Unlock()
		t.Fatal("existing count changed or another record was evicted")
	}
	loginMutex.Unlock()
	ClearLoginAttempt("locked-0", "owner")
	if blocked, _, _ := CheckLoginAttempt("192.0.2.10"); blocked {
		t.Fatal("successful login did not release capacity")
	}
}

func TestLoginAttemptNormalFailureLockoutAndRecovery(t *testing.T) {
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, make(map[string]*loginAttempt))
	const ip = "192.0.2.10"
	for failure := 0; failure < 5; failure++ {
		if blocked, _, _ := CheckLoginAttempt(ip); blocked {
			t.Fatalf("blocked before failure threshold: %d", failure)
		}
		RecordLoginFailure(ip)
	}
	if blocked, _, _ := CheckLoginAttempt(ip); !blocked {
		t.Fatal("failure threshold did not lock out address")
	}
	loginMutex.Lock()
	loginAttempts[ip].lockoutEnd = time.Now().Add(-time.Second)
	loginMutex.Unlock()
	if blocked, _, _ := CheckLoginAttempt(ip); blocked {
		t.Fatal("address remained blocked after lockout expired")
	}
	loginMutex.Lock()
	if attempt := loginAttempts[ip]; attempt.failures != 0 || !attempt.lockoutEnd.IsZero() {
		loginMutex.Unlock()
		t.Fatal("expired lockout did not reset failure count")
	}
	loginMutex.Unlock()
	RecordLoginFailureFor(ip, "owner")
	ClearLoginAttempt(ip, "owner")
	loginMutex.Lock()
	defer loginMutex.Unlock()
	if len(loginAttempts) != 0 {
		t.Fatal("successful login did not clear its failure record")
	}
}

func TestLoginAttemptDisabledProtectionDoesNotDenySaturation(t *testing.T) {
	useLoginProtectionConfig(t)
	config.GetInstance().Security.LoginLockoutDuration = 0
	useLoginAttemptRecords(t, fullActiveLockoutRecords(time.Now()))
	if blocked, _, _ := CheckLoginAttempt("192.0.2.10"); blocked {
		t.Fatal("disabled protection denied login")
	}
	if blocked, _, _ := RecordLoginFailure("192.0.2.10"); blocked {
		t.Fatal("disabled protection denied failed login")
	}
}

// A user who can log in must not reset the count of failures at another
// account's password from the same address.
func TestSuccessfulLoginClearsOnlyItsOwnFailures(t *testing.T) {
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, make(map[string]*loginAttempt))
	const ip = "192.0.2.20"
	for _, name := range []string{"admin", "admin"} {
		RecordLoginFailureFor(ip, name)
	}
	ClearLoginAttempt(ip, "viewer")
	loginMutex.Lock()
	kept := loginAttempts[ip]
	loginMutex.Unlock()
	if kept == nil || kept.failures != 2 {
		t.Fatal("another user's login cleared the failures at admin")
	}
	ClearLoginAttempt(ip, "admin")
	loginMutex.Lock()
	_, exists := loginAttempts[ip]
	loginMutex.Unlock()
	if exists {
		t.Fatal("admin's own login did not clear its failures")
	}

	// Mixed or unreadable failures stay until they expire or lock out.
	RecordLoginFailureFor(ip, "viewer")
	RecordLoginFailure(ip)
	ClearLoginAttempt(ip, "viewer")
	loginMutex.Lock()
	kept = loginAttempts[ip]
	loginMutex.Unlock()
	if kept == nil || kept.failures != 2 {
		t.Fatal("a login cleared failures that were not all its own")
	}
}
