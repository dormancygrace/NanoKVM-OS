package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func postScreenSetting(t *testing.T, body string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setScreenAs(c, authn.RoleAdmin)
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("response %s: %v", recorder.Body.String(), err)
	}
	return result.Code
}

func isolateScreenSettings(t *testing.T, keys ...string) {
	t.Helper()
	before := *common.GetScreen()
	t.Cleanup(func() {
		common.SetScreen("fps", before.FPS)
		common.SetScreen("quality", int(before.BitRate))
		common.SetScreen("gop", int(before.GOP))
	})
	for _, key := range keys {
		old := screenFileMap[key]
		screenFileMap[key] = filepath.Join(t.TempDir(), key)
		t.Cleanup(func() { screenFileMap[key] = old })
	}
}

func recordMonitorSettings(t *testing.T, result error) *[]common.MonitorSettings {
	t.Helper()
	old := applyVideoMonitorSettings
	t.Cleanup(func() { applyVideoMonitorSettings = old })
	var calls []common.MonitorSettings
	applyVideoMonitorSettings = func(update common.MonitorSettings) error {
		calls = append(calls, update)
		return result
	}
	return &calls
}

func TestSingleFPSSettingResyncsMonitorRefresh(t *testing.T) {
	isolateScreenSettings(t, "fps")
	calls := recordMonitorSettings(t, nil)
	if code := postScreenSetting(t, `{"type":"fps","value":60}`); code != 0 {
		t.Fatalf("code %d", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("monitor calls = %d, want 1", len(*calls))
	}
	got := (*calls)[0]
	if !got.SyncRefresh || got.Resolution != nil || got.Portrait != nil || got.PortraitResolution != nil {
		t.Fatalf("not a plain refresh sync: %+v", got)
	}
	if common.GetScreen().FPS != 60 {
		t.Fatal("rate not published")
	}
}

func TestSingleFPSSettingReportsRefreshSyncFailure(t *testing.T) {
	isolateScreenSettings(t, "fps")
	calls := recordMonitorSettings(t, errors.New("monitor EDID programming failed"))
	if code := postScreenSetting(t, `{"type":"fps","value":50}`); code != -4 {
		t.Fatalf("code %d, want -4", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("monitor calls = %d, want 1", len(*calls))
	}
	if common.GetScreen().FPS != 50 {
		t.Fatal("the saved stream rate must stay applied, as in the batch endpoint")
	}
}

func TestOtherSingleSettingsAndRejectedRatesDoNotTouchTheMonitor(t *testing.T) {
	isolateScreenSettings(t, "fps", "quality")
	calls := recordMonitorSettings(t, nil)
	for _, body := range []string{`{"type":"quality","value":5000}`, `{"type":"gop","value":10}`, `{"type":"fps","value":121}`} {
		postScreenSetting(t, body)
	}
	if len(*calls) != 0 {
		t.Fatalf("monitor programmed %d time(s): %+v", len(*calls), *calls)
	}
}
