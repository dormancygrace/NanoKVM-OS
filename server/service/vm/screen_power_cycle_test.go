package vm

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"
	"os"
	"testing"
)

func TestPortraitSettingsRequirePowerCycleConfirmation(t *testing.T) {
	old := screenMonitorRequiresPowerCycle
	t.Cleanup(func() { screenMonitorRequiresPowerCycle = old })
	for _, required := range []bool{true, false} {
		screenMonitorRequiresPowerCycle = func() bool { return required }
		for _, req := range []proto.SetScreenReq{
			{Type: "monitor", Value: 1080},
			{Type: "portrait", Value: 1},
			{Type: "portrait", Value: 0},
			{Type: "portrait_resolution", Value: 1280},
		} {
			for _, confirmed := range []bool{false, true} {
				req.ConfirmPowerCycle = confirmed
				result := validateScreenSettingIn(req, nil)
				blocked := result != nil && result.code == -5
				if blocked != (required && !confirmed) {
					t.Errorf("%s=%d required=%v confirmed=%v: result %+v", req.Type, req.Value, required, confirmed, result)
				}
			}
		}
	}
}

// The batch uses validateScreenSettingFor directly after #301. Confirm that
// the guard runs before an otherwise valid FPS update has any side effects.
func TestBatchPortraitConfirmationBeforeStreamWrites(t *testing.T) {
	for _, body := range []string{
		`{"portrait":true,"fps":30}`,
		`{"portrait":false,"fps":30}`,
		`{"portraitResolution":1280,"fps":30}`,
	} {
		t.Run(body, func(t *testing.T) {
			stand := newVideoStand(t, common.SharedVideoPool)
			old := screenMonitorRequiresPowerCycle
			screenMonitorRequiresPowerCycle = func() bool { return true }
			t.Cleanup(func() { screenMonitorRequiresPowerCycle = old })
			if rsp := postVideo(t, body); rsp.Code != -5 {
				t.Fatalf("response %+v", rsp)
			}
			if _, err := os.Stat(screenFileMap["fps"]); !os.IsNotExist(err) {
				t.Fatal("unconfirmed request changed FPS")
			}
			if len(stand.programmed) != 0 {
				t.Fatal("unconfirmed request changed device state")
			}
		})
	}
}
