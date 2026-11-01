package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"NanoKVM-Server/middleware"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectsInvalidVideoPreferences(t *testing.T) {
	for _, body := range []string{`{"type":"monitor","value":719}`, `{"type":"resolution","value":65536}`, `{"type":"resolution","value":-1}`, `{"type":"fps","value":121}`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		setScreenAs(c, authn.RoleAdmin)
		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code == 0 {
			t.Fatalf("accepted %s: %s", body, recorder.Body.String())
		}
	}
}

func TestStreamTypePersistsCodecForOLED(t *testing.T) {
	old := screenFileMap["type"]
	screenFileMap["type"] = filepath.Join(t.TempDir(), "type")
	defer func() { screenFileMap["type"] = old }()

	for _, test := range []struct {
		value int
		want  string
	}{
		{value: 0, want: "mjpeg"},
		{value: 1, want: "h264"},
		{value: 2, want: "h265"},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(fmt.Sprintf(`{"type":"type","value":%d}`, test.value)))
		c.Request.Header.Set("Content-Type", "application/json")
		setScreenAs(c, authn.RoleAdmin)

		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != 0 {
			t.Fatalf("value %d response %s, error %v", test.value, recorder.Body.String(), err)
		}
		value, err := os.ReadFile(screenFileMap["type"])
		if err != nil || string(value) != test.want {
			t.Fatalf("value %d saved %q, error %v", test.value, value, err)
		}
	}
}

func setScreenAs(c *gin.Context, role authn.Role) {
	// CheckToken stores the principal under this key in production.
	c.Set("principal", middleware.Principal{Username: "tester", Role: role})
	(&Service{}).SetScreen(c)
}

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

func TestUserCanReadSharedScreenSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("principal", middleware.Principal{Username: "tester", Role: authn.RoleUser})
	(&Service{}).GetScreen(c)
	var result struct {
		Code int `json:"code"`
		Data struct {
			FPS int `json:"fps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != 0 || recorder.Code != http.StatusOK {
		t.Fatalf("user GET = %d %s, error %v", recorder.Code, recorder.Body.String(), err)
	}
	if result.Data.FPS != common.GetScreen().FPS {
		t.Fatal("user GET did not return shared settings")
	}
}

func TestQualityRejectsValuesNativeReadersCannotHold(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := screenFileMap["quality"]
	screenFileMap["quality"] = filepath.Join(t.TempDir(), "qlty")
	defer func() { screenFileMap["quality"] = old }()
	for _, value := range []string{"0", "-1", "20001", "-9223372036854775808"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"type":"quality","value":`+value+`}`))
		c.Request.Header.Set("Content-Type", "application/json")
		setScreenAs(c, authn.RoleAdmin)
		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code == 0 {
			t.Fatalf("accepted quality %s: %s", value, recorder.Body.String())
		}
	}
	if _, err := os.Stat(screenFileMap["quality"]); !os.IsNotExist(err) {
		t.Fatal("rejected quality values were written")
	}
}
