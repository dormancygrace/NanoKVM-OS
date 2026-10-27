package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// monitorRates lists the refresh rates of each ordinary monitor height,
// fastest first: NanoKVM-monitor-<height>-<hz>.bin. Each profile lists no
// mode at or above its resolution other than its own timing, so the source
// cannot pick another rate.
var monitorRates = map[uint16][]int{
	720:  {120, 60, 30},
	1080: {90, 75, 60, 30},
	1440: {60, 50, 40, 30},
	2160: {30},
}

// portraitRates lists the refresh rates of each portrait profile, fastest
// first: NanoKVM-portrait-<w>x<h>-<hz>.bin.
var portraitRates = map[uint16][]int{
	portraitResolutionHD:      {120, 60, 30},
	portraitResolutionDefault: {90, 75, 60, 30},
	portraitResolutionMax:     {60, 50, 30},
}

var portraitWidths = map[uint16]uint16{
	portraitResolutionHD: 720, portraitResolutionDefault: 1080,
	portraitResolutionMax: 1440,
}

// PortraitWidth is the width of a portrait profile, 0 for an unknown one.
func PortraitWidth(resolution uint16) uint16 { return portraitWidths[resolution] }

// PortraitRates returns the refresh rates of a portrait profile, fastest first.
func PortraitRates(resolution uint16) []int {
	return append([]int(nil), portraitRates[resolution]...)
}

func slowestRateFor(rates []int, fps int) int {
	if len(rates) == 0 {
		return 0
	}
	best := rates[0]
	for _, rate := range rates {
		if rate >= fps {
			best = rate
		}
	}
	return best
}

// portraitProfilePathAt is the portrait profile for a stream rate, falling
// back to the single-rate profile.
func portraitProfilePathAt(resolution uint16, fps int) string {
	if rate := slowestRateFor(portraitRates[resolution], profileRate(fps)); rate > 0 {
		path := filepath.Join(monitorEDIDDir, fmt.Sprintf("NanoKVM-portrait-%dx%d-%d.bin",
			portraitWidths[resolution], resolution, rate))
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return portraitMonitorEDIDPath(resolution)
}

var (
	monitorEDIDDir      = "/usr/share/nanokvm/edid"
	monitorEDIDHashFile = "/etc/kvm/monitor_edid_sha256"
)

// MonitorRates returns the refresh rates offered for a monitor setting
// (0 is Auto), fastest first; nil when the refresh rate is fixed.
func MonitorRates(height uint16) []int {
	if MonitorProfileSupported() && !MonitorHighRefreshSupported() {
		// The LT6911C and LT6911D keep their single 60 Hz profiles, and take
		// the slower 16:10 and 21:9 ones.
		if height == 0 || height == 720 || height == 1080 {
			return []int{60}
		}
		return append([]int(nil), widerMonitorRatesFor(height)...)
	}
	if height == 0 {
		height = autoMonitorHeight()
	}
	if rates, ok := widerMonitorRates[height]; ok {
		return append([]int(nil), rates...)
	}
	return append([]int(nil), monitorRates[height]...)
}

// MonitorRefreshFor returns the slowest rate of a monitor setting that is not
// below fps (the fastest when fps exceeds them all); 0 when it is fixed.
func MonitorRefreshFor(height uint16, fps int) int {
	return slowestRateFor(MonitorRates(height), fps)
}

// MonitorRefreshFollowsStream reports whether applying video settings may
// rewrite the EDID to follow the stream rate, for landscape and portrait
// profiles alike. A Cube or Lite needs a power cycle after every write, so
// its monitor keeps the fastest rate of the chosen resolution instead and the
// stream setting only limits the encoder.
func MonitorRefreshFollowsStream() bool {
	return MonitorHighRefreshSupported() && !MonitorRequiresPowerCycle()
}

// profileRate is the stream rate a profile is chosen for: the setting where
// the monitor follows it, else the fastest profile of the resolution.
func profileRate(fps int) int {
	if MonitorRefreshFollowsStream() {
		return fps
	}
	return 1 << 30
}

// Auto is 1920x1080 (up to 90 Hz) wherever live EDID writes exist; QHD and
// UHD are explicit choices. Elsewhere it is the stock receiver profile.
// AutoMonitorHeight is the size Auto stands for, 0 for the stock profile.
func AutoMonitorHeight() uint16 { return autoMonitorHeight() }

func autoMonitorHeight() uint16 {
	if MonitorHighRefreshSupported() {
		return 1080
	}
	return 0
}

// monitorProfilePathAt is the monitor profile for a setting and stream rate,
// falling back to the single-rate profile when no rate profile exists.
func monitorProfilePathAt(height uint16, fps int) string {
	if MonitorHighRefreshSupported() {
		effective := height
		if effective == 0 {
			effective = autoMonitorHeight()
		}
		if rate := MonitorRefreshFor(height, profileRate(fps)); rate > 0 {
			path := monitorRateProfilePath(effective, rate)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path
			}
		}
	}
	return monitorProfilePath(height)
}

// MonitorRefreshHz is the refresh rate of the programmed ordinary profile,
// or 0 when it is not one of the rate profiles.
func MonitorRefreshHz() int {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	height := savedMonitorResolutionLocked()
	if monitorPortraitEnabledLocked() {
		resolution := savedPortraitResolutionLocked()
		for _, rate := range portraitRates[resolution] {
			path := filepath.Join(monitorEDIDDir, fmt.Sprintf("NanoKVM-portrait-%dx%d-%d.bin",
				portraitWidths[resolution], resolution, rate))
			if programmedMonitorProfileLocked(path, height) {
				return rate
			}
		}
		return 0
	}
	for _, rate := range MonitorRates(height) {
		effective := height
		if effective == 0 {
			effective = autoMonitorHeight()
		}
		path := monitorRateProfilePath(effective, rate)
		if programmedMonitorProfileLocked(path, height) {
			return rate
		}
	}
	return 0
}

// programmedMonitorProfileLocked compares a profile with the last one
// written. Before the first write, the single-rate profile of the saved
// setting is assumed; the first start programs the saved profile
// (ProgramMonitorProfileOnFirstStart), except on boards without programmable
// EDID or that need a power cycle (Cube).
func programmedMonitorProfileLocked(path string, height uint16) bool {
	want, err := fileSHA256(path)
	if err != nil {
		return false
	}
	recorded, err := os.ReadFile(monitorEDIDHashFile)
	if err != nil {
		legacy, legacyErr := fileSHA256(monitorProfilePath(height))
		return legacyErr == nil && legacy == want
	}
	return strings.TrimSpace(string(recorded)) == want
}

func recordMonitorProfileLocked(path string) {
	sum, err := fileSHA256(path)
	if err != nil {
		_ = os.Remove(monitorEDIDHashFile)
		return
	}
	if atomicWriteMonitorFile(monitorEDIDHashFile, []byte(sum+"\n"), ".monitor_edid-*") != nil {
		_ = os.Remove(monitorEDIDHashFile)
		return
	}
	// nanokvm_update_edid reads the receiver back after writing it.
	markReceiverVerifiedLocked(sum)
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
