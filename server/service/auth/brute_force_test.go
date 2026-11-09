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
	RecordLoginFailureFor("192.0.2.2", "")
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
	previousPenalty := loginPenalty
	conf.Security.LoginLockoutDuration, conf.Security.LoginMaxFailures = 3600, 5
	loginPenalty = time.Millisecond
	t.Cleanup(func() {
		conf.Security.LoginLockoutDuration, conf.Security.LoginMaxFailures = previousDuration, previousFailures
		loginPenalty = previousPenalty
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
		if blocked, code, _ := RecordLoginFailureFor(ip, ""); !blocked || code != -5 {
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

func TestLoginAttemptNormalFailureLockoutAndRecovery(t *testing.T) {
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, make(map[string]*loginAttempt))
	const ip = "192.0.2.10"
	for failure := 0; failure < 5; failure++ {
		if blocked, _, _ := CheckLoginAttempt(ip); blocked {
			t.Fatalf("blocked before failure threshold: %d", failure)
		}
		RecordLoginFailureFor(ip, "")
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
	RecordLoginFailureFor(ip, "")
	ClearLoginAttempt(ip, "viewer")
	loginMutex.Lock()
	kept = loginAttempts[ip]
	loginMutex.Unlock()
	if kept == nil || kept.failures != 2 {
		t.Fatal("a login cleared failures that were not all its own")
	}
}
