package common

import (
	"fmt"
	"path/filepath"
)

// Monitor aspect ratios. A 16:9 profile is the default; the others are
// explicit choices of an LT6911UXC with live EDID writes.
const (
	MonitorAspect169  = "16:9"
	MonitorAspect1610 = "16:10"
	MonitorAspect219  = "21:9"
)

// MonitorAspects lists the aspect ratios in the order the interface offers them.
var MonitorAspects = []string{MonitorAspect169, MonitorAspect1610, MonitorAspect219}

// monitorProfile is a landscape monitor size. Its id is what
// /etc/kvm/monitor_resolution and the API's "monitor" hold: the height of a
// 16:9 or 16:10 profile, the width of a 21:9 one (its height would be that of
// a 16:9 profile). 0 is Auto.
type monitorProfile struct {
	Width, Height uint16
	Aspect        string
}

var widerMonitorProfiles = map[uint16]monitorProfile{
	1200: {1920, 1200, MonitorAspect1610},
	1600: {2560, 1600, MonitorAspect1610},
	2560: {2560, 1080, MonitorAspect219},
	3440: {3440, 1440, MonitorAspect219},
}

// widerMonitorRates lists the refresh rates of the 16:10 and 21:9 profiles,
// fastest first: NanoKVM-monitor-<w>x<h>-<hz>.bin. 2560x1600 above 50 Hz and
// 3440x1440 above 40 Hz exceed what the capture and the encoder sustain.
var widerMonitorRates = map[uint16][]int{
	1200: {60, 50, 30},
	1600: {50, 30},
	2560: {60, 50, 30},
	3440: {40, 30},
}

// MonitorProfileIDs lists every landscape profile id with its size, Auto
// first, then by aspect ratio and size, largest first.
var MonitorProfileIDs = []uint16{0, 2160, 1440, 1080, 720, 1600, 1200, 3440, 2560}

// validMonitorProfile reports whether id names a landscape monitor profile
// (0 is Auto). The legacy 600 of the stream limits is not one.
func validMonitorProfile(id uint16) bool {
	if _, ok := widerMonitorProfiles[id]; ok {
		return true
	}
	return id == 0 || id == 720 || id == 1080 || id == 1440 || id == 2160
}

// ValidMonitorProfile is validMonitorProfile for the API.
func ValidMonitorProfile(id int) bool {
	return id >= 0 && id <= 0xffff && validMonitorProfile(uint16(id))
}

// MonitorSize is the width and height of a monitor profile id; Auto and an
// unknown id are 0x0.
func MonitorSize(id uint16) (width, height uint16) {
	if p, ok := widerMonitorProfiles[id]; ok {
		return p.Width, p.Height
	}
	if id == 0 || !validMonitorProfile(id) {
		return 0, 0
	}
	return ResolutionMap[id], id
}

// MonitorAspect is the aspect ratio of a monitor profile id.
func MonitorAspect(id uint16) string {
	if p, ok := widerMonitorProfiles[id]; ok {
		return p.Aspect
	}
	return MonitorAspect169
}

// widerMonitorProfile reports whether id is a 16:10 or 21:9 profile, which
// only an LT6911UXC with live EDID writes advertises.
func widerMonitorProfile(id uint16) bool {
	_, ok := widerMonitorProfiles[id]
	return ok
}

// WiderMonitorProfile is widerMonitorProfile for the API.
func WiderMonitorProfile(id uint16) bool { return widerMonitorProfile(id) }

// MonitorNeedsUHD reports whether a monitor size needs the UHD video memory.
func MonitorNeedsUHD(id uint16) bool { return monitorPoolNeed(id) == "uhd" }

// monitorPoolNeed names the video memory a monitor size needs: "" for up to
// 1920x1088 pixels, "qhd" up to 2560x1440, else "uhd".
func monitorPoolNeed(id uint16) string {
	width, height := MonitorSize(id)
	pixels := int(width) * int(height)
	switch {
	case pixels > 2560*1440:
		return "uhd"
	case pixels > 1920*1088:
		return "qhd"
	}
	return ""
}

// monitorRateProfilePath is the profile file of a monitor id at a refresh rate.
func monitorRateProfilePath(id uint16, rate int) string {
	if p, ok := widerMonitorProfiles[id]; ok {
		return filepath.Join(monitorEDIDDir, fmt.Sprintf("NanoKVM-monitor-%dx%d-%d.bin", p.Width, p.Height, rate))
	}
	return filepath.Join(monitorEDIDDir, fmt.Sprintf("NanoKVM-monitor-%d-%d.bin", id, rate))
}
