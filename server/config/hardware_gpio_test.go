package config

import "testing"

func TestEnhancedATXRequiresKnownBoard(t *testing.T) {
	for _, name := range []string{"", "unknown", "Alpha"} {
		h := gpioV2Hardware(HWAlpha, name)
		if h.GPIOPower != "" || h.GPIOReset != "" || h.GPIOPowerLED != "" || h.GPIOHDDLed != "" {
			t.Fatalf("unknown board retained GPIO access: %+v", h)
		}
	}
}
func TestEnhancedATXPCIeAndAlphaMapping(t *testing.T) {
	p := gpioV2Hardware(HWAlpha, "pcie\n")
	if p.Version != HWVersionPcie || p.GPIOPower != "gpio-v2:3020000.gpio:23" || p.GPIOReset != "gpio-v2:3020000.gpio:25" || p.GPIOPowerLED != "gpio-v2:3020000.gpio:24" || p.GPIOHDDLed != "" {
		t.Fatalf("PCIe mapping: %+v", p)
	}
	a := gpioV2Hardware(HWPcie, "alpha")
	if a.GPIOReset != "gpio-v2:3020000.gpio:27" || a.GPIOHDDLed != "gpio-v2:3020000.gpio:25" {
		t.Fatalf("Alpha mapping: %+v", a)
	}
}
