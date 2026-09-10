package config

import (
	"os"
	"strings"

	"NanoKVM-Server/internal/gpioio"

	log "github.com/sirupsen/logrus"
)

type HWVersion int

const (
	HWVersionAlpha HWVersion = iota
	HWVersionBeta
	HWVersionPcie

	HWVersionFile = "/etc/kvm/hw"
)

var HWAlpha = Hardware{
	Version:      HWVersionAlpha,
	GPIOReset:    "/sys/class/gpio/gpio507/value",
	GPIOPower:    "/sys/class/gpio/gpio503/value",
	GPIOPowerLED: "/sys/class/gpio/gpio504/value",
	GPIOHDDLed:   "/sys/class/gpio/gpio505/value",
}

var HWBeta = Hardware{
	Version:      HWVersionBeta,
	GPIOReset:    "/sys/class/gpio/gpio505/value",
	GPIOPower:    "/sys/class/gpio/gpio503/value",
	GPIOPowerLED: "/sys/class/gpio/gpio504/value",
	GPIOHDDLed:   "",
}

var HWPcie = Hardware{
	Version:      HWVersionPcie,
	GPIOReset:    "/sys/class/gpio/gpio505/value",
	GPIOPower:    "/sys/class/gpio/gpio503/value",
	GPIOPowerLED: "/sys/class/gpio/gpio504/value",
	GPIOHDDLed:   "",
}

func (h HWVersion) String() string {
	switch h {
	case HWVersionAlpha:
		return "Alpha"
	case HWVersionBeta:
		return "Beta"
	case HWVersionPcie:
		return "PCIE"
	default:
		return "Unknown"
	}
}

func GetHwVersion() HWVersion {
	content, err := os.ReadFile(HWVersionFile)
	if err != nil {
		return HWVersionAlpha
	}

	version := strings.ReplaceAll(string(content), "\n", "")
	switch version {
	case "alpha":
		return HWVersionAlpha
	case "beta":
		return HWVersionBeta
	case "pcie":
		return HWVersionPcie
	default:
		return HWVersionAlpha
	}
}

func getHardware() (h Hardware) {
	version := GetHwVersion()

	switch version {
	case HWVersionAlpha:
		h = HWAlpha

	case HWVersionBeta:
		h = HWBeta

	case HWVersionPcie:
		h = HWPcie

	default:
		h = HWAlpha
		log.Errorf("Unsupported hardware version: %s", version)
	}

	// The Enhanced image uses GPIO ABI v2; controller-local offsets are
	// stable even when the kernel assigns different gpiochip/global numbers.
	if image, err := os.ReadFile("/etc/nanokvm-buildroot"); err == nil && strings.Contains(string(image), "flavour=enhanced") {
		declared, err := os.ReadFile(HWVersionFile)
		if err != nil {
			log.Errorf("Enhanced ATX requires board revision: %s", err)
		}
		h = gpioV2Hardware(h, string(declared))
	}
	return
}

// Never guess an Enhanced ATX pin assignment when board detection is missing.
func gpioV2Hardware(h Hardware, declared string) Hardware {
	h.GPIOPower, h.GPIOReset, h.GPIOPowerLED, h.GPIOHDDLed = "", "", "", ""
	switch strings.TrimSpace(declared) {
	case "alpha":
		h.Version = HWVersionAlpha
		h.GPIOReset = gpioio.Path("3020000.gpio", 27)
		h.GPIOHDDLed = gpioio.Path("3020000.gpio", 25)
	case "beta":
		h.Version = HWVersionBeta
		h.GPIOReset = gpioio.Path("3020000.gpio", 25)
	case "pcie":
		h.Version = HWVersionPcie
		h.GPIOReset = gpioio.Path("3020000.gpio", 25)
	default:
		log.Error("Enhanced ATX disabled: no recognized board revision")
		return h
	}
	h.GPIOPower = gpioio.Path("3020000.gpio", 23)
	h.GPIOPowerLED = gpioio.Path("3020000.gpio", 24)
	return h
}
