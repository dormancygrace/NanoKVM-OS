package common

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	fhdClassLongSide  = 1920
	fhdClassShortSide = 1080
)

func ReadVideoValue(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return v
}

// MonitorProfileSupported describes EDID programming, not capture timings.
func MonitorProfileSupported() bool {
	board, _ := os.ReadFile("/etc/kvm/hw")
	chip, _ := os.ReadFile("/etc/kvm/hdmi_version")
	return monitorHardwareSupported(strings.TrimSpace(string(board)), strings.TrimSpace(string(chip)))
}
func monitorHardwareSupported(board, chip string) bool {
	if board == "pcie" {
		return chip == "ux"
	}
	return (board == "alpha" || board == "beta") && (chip == "c" || chip == "ux" || chip == "d")
}
func MonitorRequiresPowerCycle() bool {
	board, _ := os.ReadFile("/etc/kvm/hw")
	value := strings.TrimSpace(string(board))
	return value == "alpha" || value == "beta"
}
func MonitorHighRefreshSupported() bool {
	return MonitorProfileSupported() && !MonitorRequiresPowerCycle()
}

// Persist across software reboots: a reboot cannot prove a Cube power cycle.
// The user can dismiss the reminder after physically disconnecting power.
const monitorPowerCyclePendingFile = "/etc/kvm/monitor_power_cycle_pending"

func MonitorPowerCyclePending() bool {
	_, err := os.Stat(monitorPowerCyclePendingFile)
	return err == nil && MonitorRequiresPowerCycle()
}
func ClearMonitorPowerCyclePending() error {
	err := os.Remove(monitorPowerCyclePendingFile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsFHDClassDimensions classifies a signal after normalizing its orientation.
// The native portrait path explicitly accepts 1088x1920 (8160 16x16
// macroblocks). Other normalized dimensions follow the native FHD limit of a
// 1920-pixel long side and 1080-pixel short side.
func IsFHDClassDimensions(width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	if width == 1088 && height == 1920 {
		return true
	}
	if width < height {
		width, height = height, width
	}
	return width <= fhdClassLongSide && height <= fhdClassShortSide
}

var sourceTiming struct {
	sync.Mutex
	expires time.Time
	width   int
	height  int
}

// Cache reads away from the frame hot path. Native code independently enforces
// the same QHD cap immediately under its capture mutex.
func GetCaptureScreen() *Screen {
	next := *GetScreen()
	sourceTiming.Lock()
	if time.Now().After(sourceTiming.expires) {
		sourceTiming.width = ReadVideoValue("/run/nanokvm/width")
		sourceTiming.height = ReadVideoValue("/run/nanokvm/height")
		sourceTiming.expires = time.Now().Add(time.Second)
	}
	sourceWidth := sourceTiming.width
	sourceHeight := sourceTiming.height
	sourceTiming.Unlock()
	limit := CaptureRateLimit(sourceWidth, sourceHeight)
	if outputLimit := CaptureRateLimit(int(next.Width), int(next.Height)); outputLimit < limit {
		limit = outputLimit
	}
	if next.FPS > limit {
		next.FPS = limit
	}
	return &next
}

// CaptureRateLimit matches the native capture_rate.hpp policy. Input limits
// still apply when the output is downscaled; Auto retains the saved request.
func CaptureRateLimit(width, height int) int {
	if width <= 0 || height <= 0 {
		return 120
	}
	switch {
	case width == 720 && height == 1280:
		return 120
	case width == 1080 && height == 1920:
		return 70
	case width == 1088 && height == 1920:
		return 60
	case width == 1296 && height == 2304:
		return 50
	case width == 1440 && height == 2560:
		return 40
	case width <= 1280 && height <= 720:
		return 120
	case width <= 1920 && height <= 1080:
		return 75
	default:
		return 50
	}
}
