package common

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var monitorMutex sync.Mutex

const (
	monitorResolutionFile                = "/etc/kvm/monitor_resolution"
	monitorPortraitFile                  = "/etc/kvm/monitor_portrait"
	monitorPortraitResolutionFile        = "/etc/kvm/monitor_portrait_resolution"
	portraitMonitorEDID                  = "/usr/share/nanokvm/edid/NanoKVM-portrait-1080x1920.bin"
	portraitMaxMonitorEDID               = "/usr/share/nanokvm/edid/NanoKVM-portrait-1440x2560.bin"
	portraitHDMonitorEDID                = "/usr/share/nanokvm/edid/NanoKVM-portrait-720x1280.bin"
	portraitAVCMonitorEDID               = "/usr/share/nanokvm/edid/NanoKVM-portrait-1296x2304.bin"
	portraitResolutionAVC         uint16 = 2304
	portraitResolutionHD          uint16 = 1280
	portraitResolutionDefault     uint16 = 1920
	portraitResolutionMax         uint16 = 2560
)

// MonitorPortraitStatus returns the persisted orientation and whether this
// device has the hardware and packaged EDID required to use it.
func MonitorPortraitStatus() (enabled bool, supported bool) {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()

	return monitorPortraitEnabledLocked(), portraitSupportedLocked()
}

// MonitorPortraitEnabled reports the persisted portrait monitor setting.
func MonitorPortraitEnabled() bool {
	enabled, _ := MonitorPortraitStatus()
	return enabled
}

// PortraitSupported reports whether the portrait monitor profile can be
// applied on this device. The current native extended capture path requires
// the same 62 MiB ION carveout used by QHD until a smaller allocation is
// qualified.
func PortraitSupported() bool {
	_, supported := MonitorPortraitStatus()
	return supported
}

// PortraitResolution returns the persisted portrait profile choice. Invalid
// or missing state falls back to the known 1080x1920 profile.
func PortraitResolution() uint16 {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()

	return savedPortraitResolutionLocked()
}

// PortraitMaxSupported reports whether the maximum portrait profile can be
// applied on this device. The max profile is separately gated at 64 MiB ION.
func PortraitMaxSupported() bool {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()

	return portraitMaxSupportedLocked()
}

// ApplyMonitorResolution changes the virtual HDMI monitor, without forcing the
// HDMI source to adopt it. BIOS and operating systems may choose other timings.
func ApplyMonitorResolution(height uint16) error {
	if MonitorRequiresPowerCycle() && height != 0 && height != 720 && height != 1080 {
		return fmt.Errorf("Cube EDID profiles are limited to 720p/1080p at 60 Hz")
	}
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	_, ok := ResolutionMap[height]
	if !ok {
		return fmt.Errorf("unsupported monitor resolution")
	}
	if err := requireMonitorHardwareLocked(); err != nil {
		return err
	}

	path := monitorProfilePath(height)
	if monitorPortraitEnabledLocked() {
		// monitor_resolution remains the user's ordinary landscape profile
		// while the active EDID stays portrait.
		portraitResolution := savedPortraitResolutionLocked()
		if !portraitResolutionSupportedLocked(portraitResolution) {
			return fmt.Errorf("portrait monitor profile is unavailable")
		}
		path = portraitMonitorEDIDPath(portraitResolution)
	}
	if err := applyMonitorProfileLocked(path); err != nil {
		return err
	}
	if err := os.WriteFile(monitorResolutionFile, []byte(fmt.Sprint(height)), 0600); err != nil {
		return fmt.Errorf("monitor changed, but saving its setting failed: %w", err)
	}
	return nil
}

// ApplyMonitorPortrait changes the active HDMI EDID and persists the
// orientation. When disabling it, the last ordinary monitor profile is
// restored from monitor_resolution.
func ApplyMonitorPortrait(enabled bool) error {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()

	if err := requireMonitorHardwareLocked(); err != nil {
		return err
	}

	portraitResolution := savedPortraitResolutionLocked()
	path := portraitMonitorEDIDPath(portraitResolution)
	if !enabled {
		path = monitorProfilePath(savedMonitorResolutionLocked())
	}
	if enabled && !portraitResolutionSupportedLocked(portraitResolution) {
		return fmt.Errorf("portrait monitor profile is unavailable")
	}
	if err := applyMonitorProfileLocked(path); err != nil {
		return err
	}
	if err := persistMonitorPortraitLocked(enabled); err != nil {
		return fmt.Errorf("monitor changed, but saving portrait setting failed: %w", err)
	}
	return nil
}

// ApplyPortraitResolution changes the selected portrait profile. Selection is
// persisted while portrait is disabled; an active portrait monitor is updated
// immediately and remains on the selected portrait EDID.
func ApplyPortraitResolution(resolution uint16) error {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()

	if !validPortraitResolution(resolution) {
		return fmt.Errorf("unsupported portrait monitor resolution")
	}
	if !portraitResolutionSupportedLocked(resolution) {
		return fmt.Errorf("portrait monitor profile is unavailable")
	}
	if monitorPortraitEnabledLocked() {
		if err := applyMonitorProfileLocked(portraitMonitorEDIDPath(resolution)); err != nil {
			return err
		}
	}
	if err := persistMonitorPortraitResolutionLocked(resolution); err != nil {
		return fmt.Errorf("portrait resolution changed, but saving its setting failed: %w", err)
	}
	return nil
}

func monitorProfilePath(height uint16) string {
	if MonitorRequiresPowerCycle() {
		if height == 0 {
			height = 1080
		}
		return filepath.Join("/usr/share/nanokvm/edid", fmt.Sprintf("NanoKVM-cube-monitor-%d.bin", height))
	}
	profile := fmt.Sprintf("NanoKVM-monitor-%d.bin", height)
	if height == 0 {
		profile = "NanoKVM-stock.bin"
		if SupportsQHD() {
			profile = "NanoKVM-final-video-profiles.bin"
		}
	}
	return filepath.Join("/usr/share/nanokvm/edid", profile)
}

func portraitMonitorEDIDPath(resolution uint16) string {
	if resolution == portraitResolutionAVC {
		return portraitAVCMonitorEDID
	}
	if resolution == portraitResolutionMax {
		return portraitMaxMonitorEDID
	}
	if resolution == portraitResolutionHD {
		return portraitHDMonitorEDID
	}
	return portraitMonitorEDID
}

func requireMonitorHardwareLocked() error {
	if !MonitorProfileSupported() {
		return fmt.Errorf("EDID programming is unsupported on this board/HDMI chip")
	}
	return nil
}

func portraitStrideSupported() bool {
	value, err := os.ReadFile("/sys/module/cv181x_vi/parameters/yuv_bypass_aligned_stride")
	return err == nil && strings.TrimSpace(string(value)) == "Y"
}

func portraitSupportedLocked() bool {
	if !MonitorHighRefreshSupported() {
		return false
	}
	if !portraitStrideSupported() {
		return false
	}
	if requireMonitorHardwareLocked() != nil {
		return false
	}
	if !SupportsQHD() {
		return false
	}
	info, err := os.Stat(portraitMonitorEDID)
	return err == nil && info.Mode().IsRegular()
}

func portraitMaxSupportedLocked() bool {
	if !MonitorHighRefreshSupported() {
		return false
	}
	if requireMonitorHardwareLocked() != nil {
		return false
	}
	if !supportsIONAtLeast(64 * 1024 * 1024) {
		return false
	}
	info, err := os.Stat(portraitMaxMonitorEDID)
	return err == nil && info.Mode().IsRegular()
}

func portraitResolutionSupportedLocked(resolution uint16) bool {
	switch resolution {
	case portraitResolutionAVC:
		info, err := os.Stat(portraitAVCMonitorEDID)
		return portraitSupportedLocked() && err == nil && info.Mode().IsRegular()
	case portraitResolutionHD:
		info, err := os.Stat(portraitHDMonitorEDID)
		return portraitSupportedLocked() && err == nil && info.Mode().IsRegular()
	case portraitResolutionDefault:
		return portraitSupportedLocked()
	case portraitResolutionMax:
		return portraitMaxSupportedLocked()
	default:
		return false
	}
}

func validPortraitResolution(resolution uint16) bool {
	return resolution == portraitResolutionAVC || resolution == portraitResolutionHD || resolution == portraitResolutionDefault || resolution == portraitResolutionMax
}

func monitorPortraitEnabledLocked() bool {
	data, err := os.ReadFile(monitorPortraitFile)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(string(data))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func savedMonitorResolutionLocked() uint16 {
	value := ReadVideoValue(monitorResolutionFile)
	if value < 0 || value > int(^uint16(0)) {
		return 0
	}
	height := uint16(value)
	if _, ok := ResolutionMap[height]; !ok {
		return 0
	}
	return height
}

func savedPortraitResolutionLocked() uint16 {
	value := ReadVideoValue(monitorPortraitResolutionFile)
	if value < 0 || value > int(^uint16(0)) {
		return portraitResolutionDefault
	}
	resolution := uint16(value)
	if !validPortraitResolution(resolution) {
		return portraitResolutionDefault
	}
	return resolution
}

func applyMonitorProfileLocked(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("monitor profile is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("monitor profile is unavailable: %s is not a regular file", path)
	}
	return GetKvmVision().ApplyMonitorProfile(path)
}

func persistMonitorPortraitLocked(enabled bool) error {
	value := "0\n"
	if enabled {
		value = "1\n"
	}
	return atomicWriteMonitorFile(monitorPortraitFile, []byte(value), ".monitor_portrait-*")
}

func persistMonitorPortraitResolutionLocked(resolution uint16) error {
	return atomicWriteMonitorFile(
		monitorPortraitResolutionFile,
		[]byte(fmt.Sprintf("%d\n", resolution)),
		".monitor_portrait_resolution-*",
	)
}

func atomicWriteMonitorFile(path string, value []byte, pattern string) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	if _, err = file.Write(value); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(tempPath, path); err != nil {
		return err
	}
	return nil
}

func supportsIONAtLeast(minimum uint32) bool {
	data, err := os.ReadFile("/proc/device-tree/reserved-memory/ion/size")
	return err == nil && len(data) == 4 && binary.BigEndian.Uint32(data) >= minimum
}
