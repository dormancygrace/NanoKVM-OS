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

func TestStreamLimitDoesNotRequireMonitorHardware(t *testing.T) {
	previous := common.GetScreen().Height
	old := screenFileMap["resolution"]
	screenFileMap["resolution"] = filepath.Join(t.TempDir(), "res")
	defer func() { screenFileMap["resolution"] = old; common.SetScreen("resolution", int(previous)) }()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"type":"resolution","value":720}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setScreenAs(c, authn.RoleAdmin)
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != 0 {
		t.Fatalf("response %s, error %v", recorder.Body.String(), err)
	}
	value, err := os.ReadFile(screenFileMap["resolution"])
	if err != nil || string(value) != "720" {
		t.Fatalf("saved %q, %v", value, err)
	}
	if common.GetScreen().Height != 720 {
		t.Fatal("stream ceiling not published")
	}
}
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

// SmartP was retired: a request for it is saved and applied as NormalP, so the
// selected and active modes never differ and no restart is pending.
func TestGOPModeRequestsApplyNormalP(t *testing.T) {
	oldFile := screenFileMap["gop_mode"]
	oldReader, oldApply := readActiveGOPMode, applyGOPMode
	oldMode := common.GetScreen().GOPMode
	screenFileMap["gop_mode"] = filepath.Join(t.TempDir(), "gop_mode")
	active := common.GOPModeNormalP
	var applied []uint8
	readActiveGOPMode = func() uint8 { return active }
	applyGOPMode = func(mode uint8) int { applied = append(applied, mode); active = mode; return 0 }
	defer func() {
		screenFileMap["gop_mode"] = oldFile
		readActiveGOPMode, applyGOPMode = oldReader, oldApply
		common.SetScreen("gop_mode", int(oldMode))
	}()

	for _, mode := range []uint8{common.GOPModeSmartP, common.GOPModeNormalP} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := fmt.Sprintf(`{"type":"gop_mode","value":%d}`, mode)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		setScreenAs(c, authn.RoleAdmin)
		var result struct {
			Code int `json:"code"`
			Data struct {
				GOPMode                uint8 `json:"gopMode"`
				GOPModeActive          uint8 `json:"gopModeActive"`
				GOPModeRestartRequired bool  `json:"gopModeRestartRequired"`
			} `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != 0 {
			t.Fatalf("response %s, error %v", recorder.Body.String(), err)
		}
		if result.Data.GOPModeRestartRequired || result.Data.GOPMode != common.GOPModeNormalP ||
			result.Data.GOPModeActive != common.GOPModeNormalP {
			t.Fatalf("request for mode %d: %+v", mode, result.Data)
		}
		if value, err := os.ReadFile(screenFileMap["gop_mode"]); err != nil || string(value) != "0" {
			t.Fatalf("saved mode %q, error %v", value, err)
		}
	}
	if len(applied) != 2 || applied[0] != common.GOPModeNormalP || applied[1] != common.GOPModeNormalP {
		t.Fatalf("native switch calls %v", applied)
	}
}

func TestGOPModeRejectsInvalidValue(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"type":"gop_mode","value":2}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setScreenAs(c, authn.RoleAdmin)

	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code == 0 {
		t.Fatalf("invalid GOP mode accepted: %s", recorder.Body.String())
	}
}

func TestHDMonitorProfileReachesHardwareValidation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"type":"monitor","value":720}`))
	c.Request.Header.Set("Content-Type", "application/json")
	setScreenAs(c, authn.RoleAdmin)
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != -4 {
		t.Fatalf("HD must pass profile validation and reach absent test hardware: %s", recorder.Body.String())
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
		`{"type":"gop","value":10}`,
		`{"type":"gop_mode","value":0}`,
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

func TestAdministratorCanApplySharedFPSBitrateAndGOP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	before := *common.GetScreen()
	t.Cleanup(func() {
		common.SetScreen("fps", before.FPS)
		common.SetScreen("quality", int(before.BitRate))
		common.SetScreen("gop", int(before.GOP))
	})
	for _, key := range []string{"fps", "quality"} {
		oldPath := screenFileMap[key]
		screenFileMap[key] = filepath.Join(t.TempDir(), key)
		t.Cleanup(func() { screenFileMap[key] = oldPath })
	}
	for _, setting := range []struct {
		key   string
		value int
	}{
		{key: "fps", value: 75},
		{key: "quality", value: 5000},
		{key: "gop", value: 10},
	} {
		t.Run(setting.key, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			body := fmt.Sprintf(`{"type":%q,"value":%d}`, setting.key, setting.value)
			c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
			c.Request.Header.Set("Content-Type", "application/json")
			setScreenAs(c, authn.RoleAdmin)
			var result struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != 0 || recorder.Code != http.StatusOK {
				t.Fatalf("admin %s = %d %s, error %v", setting.key, recorder.Code, recorder.Body.String(), err)
			}
			if path, persisted := screenFileMap[setting.key]; persisted {
				if data, err := os.ReadFile(path); err != nil || string(data) != fmt.Sprint(setting.value) {
					t.Fatalf("admin %s not persisted: %q, %v", setting.key, data, err)
				}
			}
		})
	}
	if current := common.GetScreen(); current.FPS != 75 || current.BitRate != 5000 || current.GOP != 10 {
		t.Fatalf("admin settings not published: %+v", current)
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
