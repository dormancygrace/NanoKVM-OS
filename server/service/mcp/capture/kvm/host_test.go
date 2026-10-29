package kvmcapture

import (
	"context"
	"errors"
	"testing"

	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/service/hid"
)

func TestHostStatusCombinesTheSources(t *testing.T) {
	h := hostStatus{
		atx: func(context.Context) (bmc.ATXState, error) {
			return bmc.ATXState{PowerButton: true, ResetButton: true, PowerLED: true, PowerOn: true, HDD: true}, nil
		},
		hdmi: func() (bool, bool) { return true, false },
		leds: func() hid.KeyboardLedStatus { return hid.KeyboardLedStatus{Known: true, CapsLock: true} },
	}
	state := h.HostStatus(context.Background())
	if !state.Power.PowerButton || !state.Power.ResetButton || !state.Power.Known || !state.Power.On || !state.Power.DiskActivity || state.Power.Error != "" {
		t.Fatalf("power = %+v", state.Power)
	}
	if !state.Video.Capture || state.Video.Signal {
		t.Fatalf("video = %+v", state.Video)
	}
	if !state.Keyboard.Known || !state.Keyboard.CapsLock || state.Keyboard.NumLock {
		t.Fatalf("keyboard = %+v", state.Keyboard)
	}
}

// Without a trusted power LED, or when the LED cannot be read, on is not
// reported as a fact.
func TestHostStatusPowerUnknown(t *testing.T) {
	h := hostStatus{
		atx: func(context.Context) (bmc.ATXState, error) {
			return bmc.ATXState{PowerButton: true, PowerLED: true, PowerOn: true}, errors.New("LED read failed")
		},
		hdmi: func() (bool, bool) { return true, true },
		leds: func() hid.KeyboardLedStatus { return hid.KeyboardLedStatus{} },
	}
	state := h.HostStatus(context.Background())
	if state.Power.Known || state.Power.On || !state.Power.PowerButton || state.Power.Error == "" {
		t.Fatalf("power with a failed LED read = %+v", state.Power)
	}
}
