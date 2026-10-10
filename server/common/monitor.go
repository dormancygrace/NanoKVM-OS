package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var monitorMutex sync.Mutex

var (
	monitorResolutionFile         = "/etc/kvm/monitor_resolution"
	monitorPortraitFile           = "/etc/kvm/monitor_portrait"
	monitorPortraitResolutionFile = "/etc/kvm/monitor_portrait_resolution"
)

const (
	portraitMonitorEDID              = "/usr/share/nanokvm/edid/NanoKVM-portrait-1080x1920.bin"
	portraitMaxMonitorEDID           = "/usr/share/nanokvm/edid/NanoKVM-portrait-1440x2560.bin"
	portraitHDMonitorEDID            = "/usr/share/nanokvm/edid/NanoKVM-portrait-720x1280.bin"
	portraitAVCMonitorEDID           = "/usr/share/nanokvm/edid/NanoKVM-portrait-1296x2304.bin"
	portraitResolutionAVC     uint16 = 2304
	portraitResolutionHD      uint16 = 1280
	portraitResolutionDefault uint16 = 1920
	portraitResolutionMax     uint16 = 2560
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
// the QHD video memory (62 MiB, see VideoPool) until a smaller allocation is
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
	return ApplyMonitorSettings(MonitorSettings{Resolution: &height})
}

// ApplyMonitorPortrait changes the orientation and applies its final EDID.
func ApplyMonitorPortrait(enabled bool) error {
	return ApplyMonitorSettings(MonitorSettings{Portrait: &enabled})
}

// ApplyPortraitResolution saves the choice, applying it only when active.
func ApplyPortraitResolution(resolution uint16) error {
	return ApplyMonitorSettings(MonitorSettings{PortraitResolution: &resolution})
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
		if MonitorHighRefreshSupported() {
			// Auto: 1920x1080 at up to 100 Hz.
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
	if !BootedVideoPool().PortraitMax() {
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
	if err = applyMonitorPointerProfileLocked(path, WindowsPointerEnabled()); err != nil {
		return err
	}
	recordMonitorProfileLocked(path)
	return nil
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
