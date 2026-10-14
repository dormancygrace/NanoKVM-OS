package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
)

// Parallel guesses from one address must not all pass the lockout check
// before the first failure is recorded: one password check at a time, the
// next one waits for it.
func TestOneLoginAttemptPerAddress(t *testing.T) {
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, map[string]*loginAttempt{})

	release, ok, _, _ := BeginLoginAttempt("192.0.2.1")
	if !ok {
		t.Fatal("first attempt refused")
	}
	otherRelease, ok, _, _ := BeginLoginAttempt("192.0.2.2")
	if !ok {
		t.Fatal("another address was refused")
	}
	otherRelease()

	second := make(chan bool, 1)
	go func() {
		next, ok, _, _ := BeginLoginAttempt("192.0.2.1")
		if ok {
			next()
		}
		second <- ok
	}()
	select {
	case <-second:
		t.Fatal("second attempt did not wait for the first")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	release() // releasing twice is harmless
	select {
	case ok := <-second:
		if !ok {
			t.Fatal("second attempt refused after the first finished")
		}
	case <-time.After(time.Second):
		t.Fatal("second attempt still waiting after the release")
	}
}

// A queued attempt sees the failures recorded before it: queued guesses
// still stop at the lockout.
func TestQueuedAttemptMeetsTheLockout(t *testing.T) {
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, map[string]*loginAttempt{})

	release, ok, _, _ := BeginLoginAttempt("192.0.2.5")
	if !ok {
		t.Fatal("first attempt refused")
	}
	result := make(chan int, 1)
	go func() {
		next, ok, code, _ := BeginLoginAttempt("192.0.2.5")
		if ok {
			next()
		}
		result <- code
	}()
	time.Sleep(50 * time.Millisecond)
	for range 5 {
		RecordLoginFailure("192.0.2.5")
	}
	release()
	if code := <-result; code != -5 {
		t.Fatalf("queued attempt after the lockout: code %d", code)
	}
}

func TestLoginSlotWaitTimesOut(t *testing.T) {
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, map[string]*loginAttempt{})
	previous := loginSlotWait
	loginSlotWait = 50 * time.Millisecond
	t.Cleanup(func() { loginSlotWait = previous })

	release, ok, _, _ := BeginLoginAttempt("192.0.2.6")
	if !ok {
		t.Fatal("first attempt refused")
	}
	defer release()
	if _, ok, code, msg := BeginLoginAttempt("192.0.2.6"); ok || code != -5 || msg != loginInFlightMessage {
		t.Fatalf("waiting attempt: ok=%v code=%d msg=%q", ok, code, msg)
	}
}

func TestLockedAddressIsNotAdmitted(t *testing.T) {
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, map[string]*loginAttempt{
		"192.0.2.3": {failures: 5, lastFailed: time.Now(), lockoutEnd: time.Now().Add(time.Hour)},
	})
	if _, ok, code, _ := BeginLoginAttempt("192.0.2.3"); ok || code != -5 {
		t.Fatalf("locked address admitted: ok=%v code=%d", ok, code)
	}
	loginMutex.Lock()
	_, busy := loginsInFlight["192.0.2.3"]
	loginMutex.Unlock()
	if busy {
		t.Fatal("a refused attempt left an in-flight slot behind")
	}
}

// The failure counts before the delay, not after it.
func TestLoginFailureIsRecordedBeforeTheDelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useLoginProtectionConfig(t)
	useLoginAttemptRecords(t, map[string]*loginAttempt{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		var rsp proto.Response
		(&Service{}).loginFailed(c, "192.0.2.4", &rsp)
	}()
	time.Sleep(200 * time.Millisecond)
	loginMutex.Lock()
	attempt := loginAttempts["192.0.2.4"]
	failures := 0
	if attempt != nil {
		failures = attempt.failures
	}
	loginMutex.Unlock()
	if failures != 1 {
		t.Fatalf("failures during the delay = %d, want 1", failures)
	}
	<-done
}
