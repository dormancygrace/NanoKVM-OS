package vm

import (
	"context"
	"time"

	"NanoKVM-Server/pkg/bmc"
)

// ATX is the host's power buttons and LED for the BMC protocols, Redfish
// and IPMI. It drives the same GPIO lines, pulse lock and LED monitor as the
// web page.
// Power control from a BMC protocol does not take part in the browser's
// input control: like a hardware BMC, it works whoever holds the keyboard.
type ATX struct{}

// bmcConfigPath holds the "power LED connected" setting (tests move it).
var bmcConfigPath = bmc.ConfigPath

func (ATX) State(ctx context.Context) (bmc.ATXState, error) {
	hardware := atxHardware()
	ledLine := hardware.GPIOPowerLED != ""
	state := bmc.ATXState{
		PowerButton: hardware.GPIOPower != "",
		ResetButton: hardware.GPIOReset != "",
		// A LED header that is not wired to the host reads "off" always:
		// with the setting off, the state is unknown and requests press.
		PowerLED: ledLine && bmc.PowerLEDConnected(bmcConfigPath),
	}
	if ledLine {
		status, err := gpioMonitor().Current(ctx)
		if err != nil {
			return state, err
		}
		state.HDD = status.HDD
		if state.PowerLED {
			state.PowerOn = status.Power
		}
	}
	return state, nil
}

// Press holds a button for durationMs (100-60000) and returns once it is
// released.
func (ATX) Press(ctx context.Context, button string, durationMs int) error {
	hardware := atxHardware()
	device := ""
	switch button {
	case "power":
		device = hardware.GPIOPower
		if device == "" {
			return bmc.ErrNoATX
		}
	case "reset":
		device = hardware.GPIOReset
		if device == "" {
			return bmc.ErrNoReset
		}
	default:
		return errUnknownButton
	}
	if durationMs < 100 || durationMs > 60000 {
		return errPressDuration
	}
	return writeGpio(ctx, device, time.Duration(durationMs)*time.Millisecond)
}

type atxError string

func (e atxError) Error() string { return string(e) }

const (
	errUnknownButton atxError = "unknown button"
	errPressDuration atxError = "the press must last 100-60000 ms"
)
