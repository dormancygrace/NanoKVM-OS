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

func getHardware() Hardware {
	// NanoKVM OS uses GPIO ABI v2; controller-local offsets are
	// stable even when the kernel assigns different gpiochip/global numbers.
	declared, err := os.ReadFile(HWVersionFile)
	if err != nil {
		log.Errorf("ATX requires board revision: %s", err)
	}
	if profile, profileErr := os.ReadFile("/etc/kvm/board-profile"); profileErr == nil {
		declared = profile
	}
	return gpioV2Hardware(GetHwVersion(), string(declared))
}

// Never guess an ATX pin assignment when board detection is missing. Whether
// the lines are used is the ATX switch's (service/vm/atx.go).
func gpioV2Hardware(version HWVersion, declared string) Hardware {
	h := Hardware{Version: version, Board: strings.TrimSpace(declared)}
	switch h.Board {
	case "lite":
		// Base/Lite has no ATX of its own. With the KVM-B board it is wired
		// as the Full (Beta) one, as in Sipeed's firmware, which has no Lite
		// case; native application compatibility remains Beta. The ATX
		// switch is off by default here.
		h.Version = HWVersionBeta
		h.GPIOReset = gpioio.Path("3020000.gpio", 25)
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
		h.GPIOHDDLed = gpioio.Path("5021000.gpio", 3)
	default:
		log.Error("ATX disabled: no recognized board revision")
		h.Board = ""
		return h
	}
	h.GPIOPower = gpioio.Path("3020000.gpio", 23)
	h.GPIOPowerLED = gpioio.Path("3020000.gpio", 24)
	return h
}
