package picoclaw

import (
	"testing"

	"NanoKVM-Server/service/controlmode"
)

func TestApplyControlModeStatusKeepsModelMetadata(t *testing.T) {
	status := RuntimeStatus{
		ModelConfigured: true,
		ModelName:       "test-model",
		Status:          "checking",
	}

	for _, test := range []struct {
		mode       controlmode.Mode
		wantStatus string
	}{
		{mode: controlmode.ModeOff, wantStatus: "checking"},
		{mode: controlmode.ModeMCP, wantStatus: "checking"},
	} {
		rendered := applyControlModeStatus(status, controlmode.Status{Mode: test.mode})
		if !rendered.ModelConfigured || rendered.ModelName != "test-model" {
			t.Fatalf("mode %q lost model metadata: %+v", test.mode, rendered)
		}
		if rendered.Status != test.wantStatus {
			t.Fatalf("mode %q status = %q, want %q", test.mode, rendered.Status, test.wantStatus)
		}
		if rendered.Ready {
			t.Fatalf("mode %q unexpectedly changed ready to true", test.mode)
		}
		if rendered.Capabilities.DeviceWrite {
			t.Fatalf("mode %q allows PicoClaw device writes", test.mode)
		}
	}

	ready := status
	ready.Ready = true
	ready.Installed = true
	ready.Status = "ready"
	for _, test := range []struct {
		name              string
		status            controlmode.Status
		wantChat          bool
		wantReadOnlyTools bool
		wantDeviceWrite   bool
	}{
		{
			name:              "picoclaw",
			status:            controlmode.Status{Mode: controlmode.ModePicoclaw},
			wantChat:          true,
			wantReadOnlyTools: true,
			wantDeviceWrite:   true,
		},
		{name: "mcp", status: controlmode.Status{Mode: controlmode.ModeMCP}},
		{
			name:              "off",
			status:            controlmode.Status{Mode: controlmode.ModeOff},
			wantChat:          true,
			wantReadOnlyTools: true,
		},
		{
			name:   "transitioning",
			status: controlmode.Status{Mode: controlmode.ModePicoclaw, Transitioning: true},
		},
	} {
		rendered := applyControlModeStatus(ready, test.status)
		if !rendered.Ready || rendered.Status != "ready" {
			t.Fatalf("%s changed runtime readiness: %+v", test.name, rendered)
		}
		if rendered.Capabilities.Chat != test.wantChat ||
			rendered.Capabilities.ReadOnlyTools != test.wantReadOnlyTools ||
			rendered.Capabilities.DeviceWrite != test.wantDeviceWrite {
			t.Fatalf("%s capabilities = %+v, want chat=%v readOnlyTools=%v deviceWrite=%v",
				test.name,
				rendered.Capabilities,
				test.wantChat,
				test.wantReadOnlyTools,
				test.wantDeviceWrite,
			)
		}
	}
}
