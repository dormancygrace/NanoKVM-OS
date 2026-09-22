package vm

import (
	"NanoKVM-Server/common"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
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
	(&Service{}).SetScreen(c)
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
	for _, body := range []string{`{"type":"monitor","value":719}`, `{"type":"resolution","value":65536}`, `{"type":"resolution","value":-1}`, `{"type":"fps","value":61}`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&Service{}).SetScreen(c)
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
		(&Service{}).SetScreen(c)

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

func TestGOPModePendingTracksActiveMode(t *testing.T) {
	oldFile := screenFileMap["gop_mode"]
	oldReader := readActiveGOPMode
	oldMode := common.GetScreen().GOPMode
	temp := t.TempDir()
	screenFileMap["gop_mode"] = filepath.Join(temp, "gop_mode")
	readActiveGOPMode = func() uint8 { return common.GOPModeSmartP }
	defer func() {
		screenFileMap["gop_mode"] = oldFile
		readActiveGOPMode = oldReader
		common.SetScreen("gop_mode", int(oldMode))
	}()

	type response struct {
		Code int `json:"code"`
		Data struct {
			GOPMode                uint8 `json:"gopMode"`
			GOPModeActive          uint8 `json:"gopModeActive"`
			GOPModeRestartRequired bool  `json:"gopModeRestartRequired"`
		} `json:"data"`
	}
	setMode := func(value uint8) response {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		body := fmt.Sprintf(`{"type":"gop_mode","value":%d}`, value)
		c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&Service{}).SetScreen(c)
		var result response
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != 0 {
			t.Fatalf("response %s, error %v", recorder.Body.String(), err)
		}
		return result
	}
	getMode := func() response {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		(&Service{}).GetScreen(c)
		var result response
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != 0 {
			t.Fatalf("response %s, error %v", recorder.Body.String(), err)
		}
		return result
	}

	changed := setMode(common.GOPModeNormalP)
	if !changed.Data.GOPModeRestartRequired || changed.Data.GOPModeActive != common.GOPModeSmartP {
		t.Fatalf("changed mode did not report active-vs-selected pending state: %+v", changed.Data)
	}
	if value, err := os.ReadFile(screenFileMap["gop_mode"]); err != nil || string(value) != "0" {
		t.Fatalf("saved mode %q, error %v", value, err)
	}
	if got := getMode(); !got.Data.GOPModeRestartRequired || got.Data.GOPMode != common.GOPModeNormalP {
		t.Fatalf("GET did not expose selected and active modes: %+v", got.Data)
	}

	reverted := setMode(common.GOPModeSmartP)
	if reverted.Data.GOPModeRestartRequired {
		t.Fatalf("returning to the active mode still requires restart: %+v", reverted.Data)
	}
	repeated := setMode(common.GOPModeSmartP)
	if repeated.Data.GOPModeRestartRequired {
		t.Fatalf("rewriting the active mode created a false pending state: %+v", repeated.Data)
	}
}

func TestGOPModeRejectsInvalidValue(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"type":"gop_mode","value":2}`))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Service{}).SetScreen(c)

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
	(&Service{}).SetScreen(c)
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Code != -4 {
		t.Fatalf("HD must pass profile validation and reach absent test hardware: %s", recorder.Body.String())
	}
}
