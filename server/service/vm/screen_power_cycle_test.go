package vm

import (
	"NanoKVM-Server/proto"
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
