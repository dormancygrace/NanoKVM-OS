package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		// H.264 encodes up to 2560 wide: 3840x2160 and 3440x1440 need H.265.
		if m.Width > 2560 || (m.Height == 0 && m.EffectiveHeight == 2160) {
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
	if got := h264(); got.Available || got.Reason != "h264-4k" || got.MaxLongSide != 2560 || got.MaxHeight != 1920 {
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

// The "monitor" setting takes the 16:10 and 21:9 ids, checks their video
// memory by pixels, and needs a receiver that takes them.
func TestMonitorSettingValidatesWiderProfiles(t *testing.T) {
	qhd := common.VideoPool{MiB: 56}
	for value, want := range map[int]int{1200: -3, 2560: -3, 1600: -3, 3440: -3, 600: -1, 1920: -1} {
		err := validateScreenSettingFor(proto.SetScreenReq{Type: "monitor", Value: value}, nil, qhd)
		if err == nil || err.code != want {
			t.Errorf("%d: %+v, want code %d", value, err, want)
		}
	}
	// Pixels above 2560x1440 need the UHD pool, whatever the receiver.
	err := validateScreenSettingFor(proto.SetScreenReq{Type: "monitor", Value: 1600}, nil, qhd)
	if err == nil || !strings.Contains(err.msg, "2560x1600 requires the UHD video memory") {
		t.Errorf("1600 on QHD: %+v", err)
	}
	// 2560x1080 fits QHD; without a receiver that takes it, it is refused.
	err = validateScreenSettingFor(proto.SetScreenReq{Type: "monitor", Value: 2560}, nil, qhd)
	if err == nil || !strings.Contains(err.msg, "HDMI receiver does not take") {
		t.Errorf("2560 without UXC: %+v", err)
	}
	if err := validateScreenSettingFor(proto.SetScreenReq{Type: "monitor", Value: 1440}, nil, qhd); err != nil {
		t.Errorf("1440 on QHD: %+v", err)
	}
}

// The 16:10 and 21:9 modes are offered by receiver, the 16:9 set unchanged:
// an LT6911C or LT6911D takes 1920x1200 and 2560x1080 only, and the monitor
// setting is validated the same way.
func TestVideoCapabilitiesWiderProfilesPerReceiver(t *testing.T) {
	oldProg, oldLive, oldOffered := monitorProgrammable, monitorHighRefresh, widerMonitorProfileOffered
	t.Cleanup(func() {
		monitorProgrammable, monitorHighRefresh, widerMonitorProfileOffered = oldProg, oldLive, oldOffered
	})
	monitorProgrammable = func() bool { return true }
	for _, uxc := range []bool{true, false} {
		monitorHighRefresh = func() bool { return uxc }
		widerMonitorProfileOffered = func(id uint16) bool { return uxc || id == 1200 || id == 2560 }
		for _, m := range videoCapabilities()["monitor"].(gin.H)["modes"].([]monitorModeCapability) {
			if !common.WiderMonitorProfile(m.ID) {
				continue
			}
			offered := uxc || m.ID == 1200 || m.ID == 2560
			if offered == (m.Reason == reasonReceiver) {
				t.Errorf("uxc=%v: %d %+v", uxc, m.ID, m)
			}
			err := validateScreenSettingFor(proto.SetScreenReq{Type: "monitor", Value: int(m.ID)}, nil, common.VideoPool{MiB: 144})
			if offered != (err == nil) {
				t.Errorf("uxc=%v: monitor %d validation %+v", uxc, m.ID, err)
			}
		}
	}
}

// Values accepted by the public endpoint must survive the check performed
// when a video session starts, and the MJPEG session must not change bitrate.
func TestAcceptedVideoValuesSurviveCapturePreparation(t *testing.T) {
	previous := *common.GetScreen()
	oldPath := screenFileMap["quality"]
	screenFileMap["quality"] = filepath.Join(t.TempDir(), "qlty")
	t.Cleanup(func() {
		screenFileMap["quality"] = oldPath
		common.SetScreen("quality", int(previous.Quality))
		common.SetScreen("quality", int(previous.BitRate))
	})
	for _, tc := range []struct {
		body             string
		bitrate, quality uint16
	}{
		{`{"bitRate":7500}`, 7500, previous.Quality},
		{`{"quality":70}`, 7500, 70},
	} {
		if code, raw := setVideoAs(t, tc.body, authn.RoleAdmin); code != 0 {
			t.Fatalf("rejected %s: %s", tc.body, raw)
		}
		common.CheckScreen()
		actual := common.GetScreen()
		if actual.BitRate != tc.bitrate || actual.Quality != tc.quality {
			t.Fatalf("accepted %s, but capture preparation changed bitrate/quality to %d/%d, want %d/%d", tc.body, actual.BitRate, actual.Quality, tc.bitrate, tc.quality)
		}
	}
}
