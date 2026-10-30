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

// A Lite with the KVM-B board is wired as the Full (Beta) one; whether the
// lines are used is the ATX switch's.
func TestATXLiteUsesTheBetaMapping(t *testing.T) {
	l := gpioV2Hardware(HWVersionAlpha, "lite\n")
	b := gpioV2Hardware(HWVersionAlpha, "beta")
	if l.Board != "lite" || l.Version != HWVersionBeta || l.GPIOPower != b.GPIOPower || l.GPIOReset != b.GPIOReset ||
		l.GPIOPowerLED != b.GPIOPowerLED || l.GPIOHDDLed != "" || l.GPIOPower == "" {
		t.Fatalf("Lite mapping: %+v, Beta: %+v", l, b)
	}
}
func TestATXPCIeAndAlphaMapping(t *testing.T) {
	p := gpioV2Hardware(HWVersionAlpha, "pcie\n")
	if p.Version != HWVersionPcie || p.GPIOPower != "gpio-v2:3020000.gpio:23" || p.GPIOReset != "gpio-v2:3020000.gpio:25" || p.GPIOPowerLED != "gpio-v2:3020000.gpio:24" || p.GPIOHDDLed != "gpio-v2:5021000.gpio:3" {
		t.Fatalf("PCIe mapping: %+v", p)
	}
	a := gpioV2Hardware(HWVersionPcie, "alpha")
	if a.GPIOReset != "gpio-v2:3020000.gpio:27" || a.GPIOHDDLed != "gpio-v2:3020000.gpio:25" {
		t.Fatalf("Alpha mapping: %+v", a)
	}
}
