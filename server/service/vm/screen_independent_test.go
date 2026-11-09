package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"NanoKVM-Server/middleware"
	"bytes"

	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// CheckToken stores the principal under this key in production.

func TestScreenWritesRequireAdministratorBeforeSideEffects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	before := *common.GetScreen()
	oldApply := applyMjpegChroma
	applyMjpegChroma = func(uint16) int {
		t.Fatal("unauthorized request reached native chroma configuration")
		return -1
	}
	t.Cleanup(func() { applyMjpegChroma = oldApply })
	for key, oldPath := range screenFileMap {
		path := filepath.Join(t.TempDir(), key)
		if err := os.WriteFile(path, []byte("unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}
		screenFileMap[key] = path
		t.Cleanup(func() { screenFileMap[key] = oldPath })
	}

	bodies := []string{
		`{"type":"fps","value":75}`,
		`{"type":"resolution","value":720}`,
		`{"type":"quality","value":5000}`,
		`{"type":"type","value":2}`,
		`{"type":"mjpeg_chroma","value":420}`,
		`{"type":"monitor","value":720}`,
		`{"type":"portrait","value":1}`,
		`{"type":"portrait_resolution","value":1920}`,
		`{"type":"monitor_power_cycle_ack","value":0,"confirmPowerCycle":true}`,
		`{"type":"future_setting","value":1}`,
		`not json`,
	}
	for _, principal := range []struct {
		name string
		role authn.Role
		set  bool
	}{
		{name: "user", role: authn.RoleUser, set: true},
		{name: "unknown role", role: authn.Role("unknown"), set: true},
		{name: "missing principal"},
	} {
		t.Run(principal.name, func(t *testing.T) {
			for _, body := range bodies {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
				c.Request.Header.Set("Content-Type", "application/json")
				if principal.set {
					c.Set("principal", middleware.Principal{Username: "tester", Role: principal.role})
				}
				(&Service{}).SetScreen(c)
				if recorder.Code != http.StatusForbidden || !c.IsAborted() {
					t.Fatalf("%s = %d %s, want aborted 403", body, recorder.Code, recorder.Body.String())
				}
				if after := *common.GetScreen(); after != before {
					t.Fatalf("%s changed shared state: before %+v, after %+v", body, before, after)
				}
				for key, path := range screenFileMap {
					if data, err := os.ReadFile(path); err != nil || string(data) != "unchanged" {
						t.Fatalf("%s changed %s: %q, %v", body, key, data, err)
					}
				}
			}
		})
	}
}
