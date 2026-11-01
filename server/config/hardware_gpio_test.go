package config

import "testing"

func TestATXRequiresKnownBoard(t *testing.T) {
	for _, name := range []string{"", "unknown", "Alpha"} {
		h := gpioV2Hardware(HWVersionAlpha, name)
		if h.GPIOPower != "" || h.GPIOReset != "" || h.GPIOPowerLED != "" || h.GPIOHDDLed != "" || h.Board != "" {
			t.Fatalf("unknown board retained GPIO access: %+v", h)
		}
	}
}
