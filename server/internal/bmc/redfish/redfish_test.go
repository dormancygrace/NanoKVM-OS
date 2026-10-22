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

func TestSystemPowerState(t *testing.T) {
	power := &fakePower{on: true, known: true, reset: true}
	h := New("admin", "correct-horse", Info{}, power)
	system := decode(t, do(h, "GET", "/redfish/v1/Systems/1", "", admin))
	if system["PowerState"] != "On" {
		t.Fatalf("PowerState %v", system["PowerState"])
	}
	allowed := system["Actions"].(map[string]any)["#ComputerSystem.Reset"].(map[string]any)["ResetType@Redfish.AllowableValues"]
	if !reflect.DeepEqual(allowed, []any{"On", "ForceOff", "GracefulShutdown", "PowerCycle", "PushPowerButton", "ForceRestart"}) {
		t.Fatalf("reset types %v", allowed)
	}
	power.known = false
	if _, ok := decode(t, do(h, "GET", "/redfish/v1/Systems/1", "", admin))["PowerState"]; ok {
		t.Fatal("PowerState shown without a power LED")
	}
}

func TestResetActions(t *testing.T) {
	power := &fakePower{reset: true}
	h := New("admin", "correct-horse", Info{}, power)
	for _, rt := range []string{"On", "ForceOff", "GracefulShutdown", "ForceRestart", "PowerCycle", "PushPowerButton"} {
		rec := do(h, "POST", "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", `{"ResetType":"`+rt+`"}`, admin)
		if rec.Code != 204 {
			t.Fatalf("%s: %d %s", rt, rec.Code, rec.Body)
		}
	}
	if want := []string{"on", "forceoff", "soft", "reset", "cycle", "push"}; !reflect.DeepEqual(power.calls, want) {
		t.Fatalf("calls %v", power.calls)
	}
	if rec := do(h, "POST", "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", `{"ResetType":"Nmi"}`, admin); rec.Code != 400 {
		t.Fatalf("Nmi: %d", rec.Code)
	}
	if rec := do(h, "GET", "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", "", admin); rec.Code != 405 {
		t.Fatalf("GET action: %d", rec.Code)
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
	for i := 0; i < maxFailures; i++ {
		do(h, "GET", "/redfish/v1/Systems/1", "", map[string]string{"basic": "admin:guess"})
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
