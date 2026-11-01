package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeCapture reports a capture size and a stream limit, and keeps the saved
// type and stream limit files in a temporary directory.
func fakeCapture(t *testing.T, width, height, limit int) {
	t.Helper()
	oldInput, oldMonitor, previous := captureInput, applyVideoMonitorSettings, common.GetScreen().Height
	oldFiles := map[string]string{}
	for _, key := range []string{"type", "resolution"} {
		oldFiles[key] = screenFileMap[key]
		screenFileMap[key] = filepath.Join(t.TempDir(), key)
	}
	captureInput = func() (int, int) { return width, height }
	applyVideoMonitorSettings = func(common.MonitorSettings) error { return nil }
	common.SetScreen("resolution", limit)
	t.Cleanup(func() {
		captureInput, applyVideoMonitorSettings = oldInput, oldMonitor
		for key, path := range oldFiles {
			screenFileMap[key] = path
		}
		common.SetScreen("resolution", int(previous))
	})
}

func TestVideoSettingsRefuseMjpegByTheResultingInput(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		width, height, limit int
		body                 string
		allowed              bool
	}{
		{"4K input", 3840, 2160, 0, `{"type":0}`, false},
		{"4K input, 2160 limit", 3840, 2160, 2160, `{"type":0,"quality":80}`, false},
		{"limit set with it", 3840, 2160, 0, `{"type":0,"height":1440}`, false},
		{"limit lifted with it", 3840, 2160, 1440, `{"type":0,"height":0}`, false},
		{"4K input, 1440p limit", 3840, 2160, 1440, `{"type":0}`, false},
		{"1440p input, limit set with it", 2560, 1440, 0, `{"type":0,"height":1080}`, true},
		{"4K monitor set with it", 1920, 1080, 0, `{"type":0,"monitor":2160}`, false},
		{"4K monitor set with it, limited", 1920, 1080, 0, `{"type":0,"monitor":2160,"height":1080}`, false},
		{"1080p monitor set with it", 3840, 2160, 1080, `{"type":0,"monitor":1080}`, true},
		{"1080p", 1920, 1080, 0, `{"type":0}`, true},
		{"no signal", 0, 0, 0, `{"type":0}`, true},
		{"H.265 at 4K", 3840, 2160, 0, `{"type":2}`, true},
		{"H.264 at 4K, limited", 3840, 2160, 1440, `{"type":1,"height":1440}`, true},
	} {
		fakeCapture(t, tc.width, tc.height, tc.limit)
		code, raw := setVideoAs(t, tc.body, authn.RoleAdmin)
		if (code == 0) != tc.allowed {
			t.Errorf("%s: %s: code %d, allowed %v: %s", tc.name, tc.body, code, tc.allowed, raw)
		}
		if !tc.allowed {
			for _, key := range []string{"type", "resolution"} {
				if _, err := os.Stat(screenFileMap[key]); !os.IsNotExist(err) {
					t.Errorf("%s: a refused request wrote %s", tc.name, key)
				}
			}
		}
	}
}

func TestVideoSettingsRefuseH264ByTheResultingPicture(t *testing.T) {
	oldPool := bootedVideoPool
	bootedVideoPool = func() common.VideoPool { return common.SharedVideoPool }
	t.Cleanup(func() { bootedVideoPool = oldPool })
	for _, tc := range []struct {
		name                 string
		width, height, limit int
		body                 string
		allowed              bool
	}{
		{"4K input", 3840, 2160, 0, `{"type":1}`, false},
		{"4K input, 2160 limit", 3840, 2160, 2160, `{"type":1}`, false},
		{"4K input, 1440 limit", 3840, 2160, 1440, `{"type":1}`, true},
		{"limit set with it", 3840, 2160, 0, `{"type":1,"height":1080}`, true},
		{"limit lifted with it", 3840, 2160, 1440, `{"type":1,"height":0}`, false},
		{"4K monitor set with it", 1920, 1080, 0, `{"type":1,"monitor":2160}`, false},
		{"4K monitor set with it, limited", 1920, 1080, 0, `{"type":1,"monitor":2160,"height":1440}`, true},
		{"1440p monitor set with it", 3840, 2160, 0, `{"type":1,"monitor":1440}`, true},
		{"1440p", 2560, 1440, 0, `{"type":1}`, true},
		{"ultrawide", 3440, 1440, 0, `{"type":1}`, false},
		{"ultrawide, 1440 limit", 3440, 1440, 1440, `{"type":1}`, true},
		{"portrait 1440x2560", 1440, 2560, 0, `{"type":1}`, false},
		{"portrait 1440x2560, 1080 limit", 1440, 2560, 1080, `{"type":1}`, true},
		{"portrait 1440x2560, 1440 limit", 1440, 2560, 1440, `{"type":1}`, false},
		{"portrait 1080x1920", 1080, 1920, 0, `{"type":1}`, true},
		{"no signal", 0, 0, 0, `{"type":1}`, true},
		{"H.265 at 4K", 3840, 2160, 0, `{"type":2}`, true},
	} {
		fakeCapture(t, tc.width, tc.height, tc.limit)
		code, raw := setVideoAs(t, tc.body, authn.RoleAdmin)
		if (code == 0) != tc.allowed {
			t.Errorf("%s: %s: code %d, allowed %v: %s", tc.name, tc.body, code, tc.allowed, raw)
		}
		if !tc.allowed {
			for _, key := range []string{"type", "resolution"} {
				if _, err := os.Stat(screenFileMap[key]); !os.IsNotExist(err) {
					t.Errorf("%s: a refused request wrote %s", tc.name, key)
				}
			}
		}
	}
}

func TestH264BatchUsesResolvedMonitorState(t *testing.T) {
	previous := videoMonitorStateAfter
	t.Cleanup(func() { videoMonitorStateAfter = previous })
	for _, tc := range []struct {
		body    string
		initial common.MonitorState
		allowed bool
	}{
		{`{"type":1,"portraitResolution":2560}`, common.MonitorState{Resolution: 1080, Portrait: true, PortraitResolution: 1920}, false},
		{`{"type":1,"portrait":false}`, common.MonitorState{Resolution: 1080, Portrait: true, PortraitResolution: 2560}, true},
		{`{"type":1,"monitor":1080}`, common.MonitorState{Resolution: 2160, Portrait: true, PortraitResolution: 2560}, false},
	} {
		fakeCapture(t, 1440, 2560, 0)
		videoMonitorStateAfter = func(update common.MonitorSettings) common.MonitorState {
			state := tc.initial
			if update.Resolution != nil {
				state.Resolution = *update.Resolution
			}
			if update.Portrait != nil {
				state.Portrait = *update.Portrait
			}
			if update.PortraitResolution != nil {
				state.PortraitResolution = *update.PortraitResolution
			}
			return state
		}
		var req VideoSettingsReq
		if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
			t.Fatal(err)
		}
		if failure := h264SelectionFailure(&req); (failure == nil) != tc.allowed {
			t.Errorf("%s: allowed %v, failure %v", tc.body, tc.allowed, failure)
		}
	}
}
