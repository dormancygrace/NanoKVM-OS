package network

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func apHarness(t *testing.T, available bool, key string) {
	t.Helper()
	oldAvailable, oldPassword, oldPenalty := apAvailable, apPassword, apPenalty
	apAvailable = func(*gin.Context) bool { return available }
	apPassword = func() string { return key }
	apPenalty = 0
	apGuard = apKeyGuard{}
	t.Cleanup(func() { apAvailable, apPassword, apPenalty = oldAvailable, oldPassword, oldPenalty })
}

// verify returns the response code, or -100 for a response that is not
// JSON; it does not fail the test, so goroutines may call it.
func verify(key string) int {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/network/wifi/verify", nil)
	c.Request.Header.Set("X-AP-Key", key)
	(&Service{}).VerifyApLogin(c)
	var rsp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rsp); err != nil {
		return -100
	}
	return rsp.Code
}

func TestApKeyOnlyOnTheAccessPoint(t *testing.T) {
	// Off the AP (Ethernet, a VPN) the right key does not help.
	apHarness(t, false, "11223344")
	if code := verify("11223344"); code != -1 {
		t.Fatalf("code %d, want -1", code)
	}
	apHarness(t, true, "11223344")
	if code := verify("11223344"); code != 0 {
		t.Fatalf("code %d, want 0", code)
	}
}

func TestApKeyGuessesLockUntilANewKey(t *testing.T) {
	apHarness(t, true, "11223344")
	var wg sync.WaitGroup
	codes := make(chan int, 50)
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- verify("00000000")
		}()
	}
	wg.Wait()
	close(codes)
	wrong := 0
	for code := range codes {
		if code == -4 {
			wrong++
		} else if code != -5 {
			t.Fatalf("code %d", code)
		}
	}
	// Parallel guesses are counted under the lock: no more than the limit
	// are ever compared.
	if wrong != apKeyMaxFailures-1 {
		t.Fatalf("%d guesses answered unauthorized, want %d", wrong, apKeyMaxFailures-1)
	}
	if code := verify("11223344"); code != -5 {
		t.Fatalf("the right key after the lockout: code %d, want -5", code)
	}
	// kvm_system restarts provisioning with a new key.
	apPassword = func() string { return "55667788" }
	if code := verify("55667788"); code != 0 {
		t.Fatalf("the new key: code %d", code)
	}
}

func TestApKeyEmptyNeverMatches(t *testing.T) {
	apHarness(t, true, "")
	if code := verify(""); code != -4 {
		t.Fatalf("code %d, want -4", code)
	}
}

func TestLegacyProfileValidation(t *testing.T) {
	for _, bad := range []struct{ ssid, password string }{
		{"home", "short"},
		{"home", "with a\nbreak"},
		{"", "longenough"},
		{"home", string(make([]byte, 64))},
	} {
		if legacyProfile(bad.ssid, bad.password).validate() == nil {
			t.Fatalf("accepted %q / %q", bad.ssid, bad.password)
		}
	}
	if err := legacyProfile("home", "longenough").validate(); err != nil {
		t.Fatal(err)
	}
}
