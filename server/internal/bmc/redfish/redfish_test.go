package redfish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"

	"testing"
	"time"
)

type fakePower struct {
	on, known, reset bool
	calls            []string
}

func (f *fakePower) Status(context.Context) (bool, bool, error)    { return f.on, f.known, nil }
func (f *fakePower) Available(context.Context) (bool, bool, error) { return true, f.reset, nil }
func (f *fakePower) record(name string) error                      { f.calls = append(f.calls, name); return nil }
func (f *fakePower) On(context.Context) error                      { return f.record("on") }
func (f *fakePower) ForceOff(context.Context) error                { return f.record("forceoff") }
func (f *fakePower) Soft(context.Context) error                    { return f.record("soft") }
func (f *fakePower) Push(context.Context) error                    { return f.record("push") }
func (f *fakePower) Reset(context.Context) error                   { return f.record("reset") }
func (f *fakePower) Cycle(context.Context) error                   { return f.record("cycle") }

func do(h http.Handler, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.7:50000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	if user, ok := header["basic"]; ok {
		name, pass, _ := strings.Cut(user, ":")
		req.SetBasicAuth(name, pass)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return v
}

var admin = map[string]string{"basic": "admin:correct-horse"}

func TestServiceRootIsOpenAndSystemsAreNot(t *testing.T) {
	h := New("admin", "correct-horse", Info{UUID: "u"}, &fakePower{})
	rec := do(h, "GET", "/redfish/v1/", "", nil)
	if rec.Code != 200 || rec.Header().Get("OData-Version") != "4.0" {
		t.Fatalf("root: %d %v", rec.Code, rec.Header())
	}
	if decode(t, rec)["Systems"].(map[string]any)["@odata.id"] != "/redfish/v1/Systems" {
		t.Fatal("root has no Systems link")
	}
	if rec := do(h, "GET", "/redfish/v1/Systems/1", "", nil); rec.Code != 401 {
		t.Fatalf("unauthenticated system: %d", rec.Code)
	}
	if rec := do(h, "GET", "/redfish/v1/Systems/1", "", map[string]string{"basic": "admin:wrong"}); rec.Code != 401 {
		t.Fatalf("wrong password: %d", rec.Code)
	}
}

func TestSessions(t *testing.T) {
	h := New("admin", "correct-horse", Info{}, &fakePower{})
	rec := do(h, "POST", "/redfish/v1/SessionService/Sessions", `{"UserName":"admin","Password":"correct-horse"}`, nil)
	if rec.Code != 201 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	token, location := rec.Header().Get("X-Auth-Token"), rec.Header().Get("Location")
	if token == "" || !strings.HasPrefix(location, "/redfish/v1/SessionService/Sessions/") {
		t.Fatalf("token %q location %q", token, location)
	}
	auth := map[string]string{"X-Auth-Token": token}
	if rec := do(h, "GET", "/redfish/v1/Managers/1", "", auth); rec.Code != 200 {
		t.Fatalf("with token: %d", rec.Code)
	}
	if rec := do(h, "DELETE", location, "", auth); rec.Code != 204 {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := do(h, "GET", "/redfish/v1/Managers/1", "", auth); rec.Code != 401 {
		t.Fatalf("after logout: %d", rec.Code)
	}
}

func TestFailedLoginsLockOut(t *testing.T) {
	h := New("admin", "correct-horse", Info{}, &fakePower{})
	var heard []string
	h.OnLockout = func(addr string, failures int, _ time.Duration) { heard = append(heard, addr) }
	h.OnLogin = func(user, addr string) { heard = append(heard, user+"@"+addr) }
	do(h, "GET", "/redfish/v1/Systems/1", "", admin)
	do(h, "GET", "/redfish/v1/Systems/1", "", admin) // heard once
	for i := 0; i < maxFailures; i++ {
		do(h, "GET", "/redfish/v1/Systems/1", "", map[string]string{"basic": "admin:guess"})
	}
	if !reflect.DeepEqual(heard, []string{"admin@192.0.2.7", "192.0.2.7"}) {
		t.Fatalf("heard %q", heard)
	}
	if rec := do(h, "GET", "/redfish/v1/Systems/1", "", admin); rec.Code != 429 {
		t.Fatalf("locked out client got %d", rec.Code)
	}
	if rec := do(h, "POST", "/redfish/v1/SessionService/Sessions", `{"UserName":"admin","Password":"correct-horse"}`, nil); rec.Code != 429 {
		t.Fatalf("locked out login got %d", rec.Code)
	}
}

func TestForgedForwardedForDoesNotResetTheLimit(t *testing.T) {
	h := New("admin", "correct-horse", Info{}, &fakePower{})
	for i := 0; i < maxFailures; i++ {
		do(h, "GET", "/redfish/v1/Systems/1", "", map[string]string{"basic": "admin:guess", "X-Forwarded-For": fmt.Sprintf("203.0.113.%d", i)})
	}
	if rec := do(h, "GET", "/redfish/v1/Systems/1", "", map[string]string{"basic": "admin:correct-horse", "X-Forwarded-For": "203.0.113.99"}); rec.Code != 429 {
		t.Fatalf("a new X-Forwarded-For escaped the limit: %d", rec.Code)
	}
}

// Peers that never come back expire when another one fails.

// Parallel logins must not all pass the lockout check before the first
// failure is counted: the attempt is reserved with the check.

// Refunds belong to the original reservation, even if the table has since
// replaced its record after expiry or eviction.

// One IPv6 host must not rotate through its /64 to get more guesses.

// A cross-site form cannot drive power actions with cached Basic credentials.
func TestCrossSiteWritesAreRefused(t *testing.T) {
	power := &fakePower{reset: true}
	h := New("admin", "correct-horse", Info{}, power)
	form := func(header map[string]string) int {
		req := httptest.NewRequest("POST", resetActionPath, strings.NewReader(`{"ResetType":"ForceOff"}`))
		req.RemoteAddr = "192.0.2.7:50000"
		req.SetBasicAuth("admin", "correct-horse")
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := form(map[string]string{"Content-Type": "text/plain"}); code != 415 {
		t.Fatalf("text/plain POST got %d", code)
	}
	if code := form(map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"}); code != 403 {
		t.Fatalf("cross-site POST got %d", code)
	}
	if len(power.calls) != 0 {
		t.Fatalf("refused requests pressed buttons: %v", power.calls)
	}
	if code := form(map[string]string{"Content-Type": "application/json; charset=utf-8"}); code != 204 {
		t.Fatalf("JSON POST got %d", code)
	}
}
