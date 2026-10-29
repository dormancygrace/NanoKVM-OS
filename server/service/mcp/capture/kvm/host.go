package kvmcapture

import (
	"context"

	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/service/hid"
	mcpservice "NanoKVM-Server/service/mcp"
	"NanoKVM-Server/service/vm"
)

// hostStatus reads what the web page shows of the host: the ATX state the
// BMC protocols use, the HDMI state and the keyboard LEDs. Nothing here
// presses a button or takes the input.
type hostStatus struct {
	atx  func(context.Context) (bmc.ATXState, error)
	hdmi func() (enabled, signal bool)
	leds func() hid.KeyboardLedStatus
}

func NewHostStatus() mcpservice.HostStatusReader {
	return hostStatus{
		atx: vm.ATX{}.State,
		hdmi: func() (bool, bool) {
			state := vm.HdmiState()
			return state.Enabled, state.Signal
		},
		leds: hid.GetKeyboardLedStatus,
	}
}

func (h hostStatus) HostStatus(ctx context.Context) mcpservice.HostState {
	var state mcpservice.HostState
	// A failed LED read still tells which buttons exist.
	atx, err := h.atx(ctx)
	state.Power = mcpservice.HostPower{
		PowerButton: atx.PowerButton, ResetButton: atx.ResetButton,
		Known: atx.PowerLED && err == nil, On: atx.PowerOn && err == nil, DiskActivity: atx.HDD,
	}
	if err != nil {
		state.Power.Error = err.Error()
	}
	state.Video.Capture, state.Video.Signal = h.hdmi()
	leds := h.leds()
	state.Keyboard = mcpservice.HostKeyboard{
		Known: leds.Known, CapsLock: leds.CapsLock, NumLock: leds.NumLock, ScrollLock: leds.ScrollLock,
	}
	return state
}
