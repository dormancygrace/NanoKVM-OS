package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"NanoKVM-Server/middleware"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func setVideoAs(t *testing.T, body string, role authn.Role) (code int, raw string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("principal", middleware.Principal{Username: "tester", Role: role})
	(&Service{}).SetVideoSettings(c)
	var result struct {
		Code int `json:"code"`
	}
	if recorder.Code == 403 {
		return -403, recorder.Body.String()
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("response %q: %v", recorder.Body.String(), err)
	}
	return result.Code, recorder.Body.String()
}

func TestVideoSettingsOrderWritesMonitorLast(t *testing.T) {
	fps, monitor, height, bitrate := 30, 1080, 720, 5000
	portrait := false
	req := VideoSettingsReq{Monitor: &monitor, FPS: &fps, Height: &height, BitRate: &bitrate, Portrait: &portrait}
	var order []string
	for _, setting := range req.settings() {
		order = append(order, setting.Type)
	}
	want := []string{"quality", "resolution", "fps", "portrait", "monitor"}
	if len(order) != len(want) {
		t.Fatalf("order %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
}

func TestVideoSettingsRejectBeforeApplying(t *testing.T) {
	previous := *common.GetScreen()
	files := map[string]string{}
	for _, key := range []string{"resolution", "fps"} {
		files[key] = screenFileMap[key]
		screenFileMap[key] = filepath.Join(t.TempDir(), key)
	}
	defer func() {
		for key, path := range files {
			screenFileMap[key] = path
		}
		common.SetScreen("resolution", int(previous.Height))
		common.SetScreen("fps", previous.FPS)
	}()
	// A valid stream limit with an invalid rate: nothing may change.
	if code, raw := setVideoAs(t, `{"height":720,"fps":5}`, authn.RoleAdmin); code == 0 {
		t.Fatalf("accepted %s", raw)
	}
	if _, err := os.Stat(screenFileMap["resolution"]); !os.IsNotExist(err) {
		t.Fatal("a rejected batch wrote the stream limit")
	}
	if code, raw := setVideoAs(t, `{"height":720,"fps":30}`, authn.RoleAdmin); code != 0 {
		t.Fatalf("rejected %s", raw)
	}
	for key, want := range map[string]string{"resolution": "720", "fps": "30"} {
		if value, err := os.ReadFile(screenFileMap[key]); err != nil || string(value) != want {
			t.Fatalf("%s saved %q, %v", key, value, err)
		}
	}
	if common.GetScreen().FPS != 30 || common.GetScreen().Height != 720 {
		t.Fatal("settings not published")
	}
}

func TestVideoSettingsRequireAdministrator(t *testing.T) {
	if code, _ := setVideoAs(t, `{"fps":30}`, authn.RoleUser); code != -403 {
		t.Fatalf("code %d", code)
	}
}

func TestVideoSettingsSeparateQualityAndBitrate(t *testing.T) {
	for _, body := range []string{`{"quality":101}`, `{"bitRate":100}`, `{"bitRate":20001}`} {
		if code, raw := setVideoAs(t, body, authn.RoleAdmin); code == 0 {
			t.Fatalf("%s accepted: %s", body, raw)
		}
	}
}

func TestVideoCapabilitiesListEveryChoice(t *testing.T) {
	caps := videoCapabilities()
	monitor := caps["monitor"].(gin.H)
	modes := monitor["modes"].([]monitorModeCapability)
	if len(modes) != 5 || modes[0].Height != 0 || modes[1].Height != 2160 {
		t.Fatalf("modes %+v", modes)
	}
	for _, m := range modes {
		if !m.Available && m.Reason == "" {
			t.Fatalf("unavailable mode without a reason: %+v", m)
		}
	}
	tiers := caps["stream"].(gin.H)["rateTiers"].([]common.CaptureRateTier)
	if tiers[len(tiers)-1].FPS != 30 {
		t.Fatalf("tiers %+v", tiers)
	}
	if got := caps["transports"].(gin.H)["webrtc"].([]string); len(got) != 2 || got[1] != "h265" {
		t.Fatalf("WebRTC codecs %v", got)
	}
}

func TestVideoSettingsBatchesMonitorChanges(t *testing.T) {
	old := applyVideoMonitorSettings
	t.Cleanup(func() { applyVideoMonitorSettings = old })
	calls := 0
	applyVideoMonitorSettings = func(update common.MonitorSettings) error {
		calls++
		if update.Resolution == nil || *update.Resolution != 1080 || update.Portrait == nil || *update.Portrait {
			t.Fatalf("not the final combined monitor request: %+v", update)
		}
		return nil
	}
	if code, raw := setVideoAs(t, `{"portrait":false,"monitor":1080}`, authn.RoleAdmin); code != 0 {
		t.Fatalf("rejected: %s", raw)
	}
	if calls != 1 {
		t.Fatalf("monitor calls = %d, want 1", calls)
	}
}

func TestVideoCapabilitiesListModeCodecs(t *testing.T) {
	modes := videoCapabilities()["monitor"].(gin.H)["modes"].([]monitorModeCapability)
	for _, m := range modes {
		want := []string{"h264", "h265"}
		if m.Height == 2160 || (m.Height == 0 && m.EffectiveHeight == 2160) {
			want = []string{"h265"}
		}
		if len(m.Codecs) != len(want) || m.Codecs[0] != want[0] {
			t.Errorf("mode %d (Auto %d): codecs %v, want %v", m.Height, m.EffectiveHeight, m.Codecs, want)
		}
	}
	if got := modeCodecs(3840, 2160); len(got) != 1 || got[0] != "h265" {
		t.Errorf("3840x2160 codecs %v", got)
	}
	if got := modeCodecs(2560, 1440); len(got) != 2 {
		t.Errorf("2560x1440 codecs %v", got)
	}
	if got := modeCodecs(0, 0); len(got) != 2 {
		t.Errorf("stock Auto codecs %v", got)
	}
}

func TestVideoCapabilitiesMarkH264UnavailableAt4K(t *testing.T) {
	h264 := func() h264Capability {
		return videoCapabilities()["stream"].(gin.H)["h264"].(h264Capability)
	}
	fakeCapture(t, 3840, 2160, 0)
	if got := h264(); got.Available || got.Reason != "h264-4k" || got.MaxLongSide != 2560 || got.MaxHeight != 2304 {
		t.Fatalf("4K: %+v", got)
	}
	common.SetScreen("resolution", 1440)
	if got := h264(); !got.Available || got.Reason != "" {
		t.Fatalf("4K limited to 1440p: %+v", got)
	}
	fakeCapture(t, 2560, 1440, 0)
	if got := h264(); !got.Available {
		t.Fatalf("1440p: %+v", got)
	}
}
