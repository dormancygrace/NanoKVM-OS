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
// fastest first: NanoKVM-monitor-<height>-<hz>.bin. A slower profile omits the
// faster modes of its resolution, so the source cannot pick them.
var monitorRates = map[uint16][]int{
	720:  {120, 60, 30},
	1080: {100, 75, 60, 30},
	1440: {60, 50, 40, 30},
	2160: {30},
}

// portraitRates lists the refresh rates of each portrait profile, fastest
// first: NanoKVM-portrait-<w>x<h>-<hz>.bin.
var portraitRates = map[uint16][]int{
	portraitResolutionHD:      {120, 60, 30},
	portraitResolutionDefault: {100, 75, 60, 30},
	portraitResolutionAVC:     {60, 50, 30},
	portraitResolutionMax:     {60, 50, 30},
}

var portraitWidths = map[uint16]uint16{
	portraitResolutionHD: 720, portraitResolutionDefault: 1080,
	portraitResolutionAVC: 1296, portraitResolutionMax: 1440,
}

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
	if rate := slowestRateFor(portraitRates[resolution], fps); rate > 0 {
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
	if MonitorRequiresPowerCycle() {
		// Cube receivers keep their single 60 Hz profiles.
		if height == 0 || height == 720 || height == 1080 {
			return []int{60}
		}
		return nil
	}
	if height == 0 {
		height = autoMonitorHeight()
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
// profiles alike. Cube receivers need a power cycle after every write.
func MonitorRefreshFollowsStream() bool {
	return MonitorHighRefreshSupported()
}

// Auto is 1920x1080 (up to 100 Hz) wherever live EDID writes exist; QHD and
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
	if !MonitorRequiresPowerCycle() {
		effective := height
		if effective == 0 {
			effective = autoMonitorHeight()
		}
		if rate := MonitorRefreshFor(height, fps); rate > 0 {
			path := filepath.Join(monitorEDIDDir, fmt.Sprintf("NanoKVM-monitor-%d-%d.bin", effective, rate))
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path
			}
		}
	}
	return monitorProfilePath(height)
}

// SyncMonitorRefresh rewrites the ordinary monitor profile when the stream
// rate selects another refresh rate. It reports whether the EDID changed.
func SyncMonitorRefresh(fps int) (bool, error) {
	if !MonitorRefreshFollowsStream() {
		return false, nil
	}
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	height := savedMonitorResolutionLocked()
	target := monitorProfilePathAt(height, fps)
	if monitorPortraitEnabledLocked() {
		target = portraitProfilePathAt(savedPortraitResolutionLocked(), fps)
	}
	if programmedMonitorProfileLocked(target, height) {
		return false, nil
	}
	return true, applyMonitorProfileLocked(target)
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
		path := filepath.Join(monitorEDIDDir, fmt.Sprintf("NanoKVM-monitor-%d-%d.bin", effective, rate))
		if programmedMonitorProfileLocked(path, height) {
			return rate
		}
	}
	return 0
}

// programmedMonitorProfileLocked compares a profile with the last one
// written. Before the first write, the single-rate profile of the saved
// setting is assumed, which matches the profile installs programmed.
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
	}
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
