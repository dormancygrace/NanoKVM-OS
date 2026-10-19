package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/common"
	"encoding/json"
	"path/filepath"
	"testing"
)

// videoStand fakes a device for SetVideoSettings: the booted video pool, the
// screen files and the EDID programming.
type videoStand struct {
	programmed []common.MonitorSettings
}

func newVideoStand(t *testing.T, pool common.VideoPool) *videoStand {
	t.Helper()
	stand := &videoStand{}
	oldApply, oldPool := applyVideoMonitorSettings, bootedVideoPool
	oldScreen := map[string]string{}
	for key, path := range screenFileMap {
		oldScreen[key] = path
		screenFileMap[key] = filepath.Join(t.TempDir(), key)
	}
	previous := *common.GetScreen()
	t.Cleanup(func() {
		applyVideoMonitorSettings, bootedVideoPool = oldApply, oldPool
		for key, path := range oldScreen {
			screenFileMap[key] = path
		}
		common.SetScreen("fps", previous.FPS)
	})
	bootedVideoPool = func() common.VideoPool { return pool }
	applyVideoMonitorSettings = func(update common.MonitorSettings) error {
		stand.programmed = append(stand.programmed, update)
		return nil
	}
	return stand
}

type videoResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func postVideo(t *testing.T, body string) videoResponse {
	t.Helper()
	_, raw := setVideoAs(t, body, authn.RoleAdmin)
	var rsp videoResponse
	if err := json.Unmarshal([]byte(raw), &rsp); err != nil {
		t.Fatalf("response %q: %v", raw, err)
	}
	return rsp
}
