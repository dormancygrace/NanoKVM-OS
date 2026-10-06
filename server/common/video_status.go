package common

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

// applyCaptureFPS tells native capture the effective stream rate, so VPSS can
// drop surplus input frames. Set by the native binding; a no-op in tests.
var (
	applyCaptureFPS   = func(int) {}
	appliedCaptureFPS atomic.Int32
)

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
	if appliedCaptureFPS.Swap(int32(next.FPS)) != int32(next.FPS) {
		applyCaptureFPS(next.FPS)
	}
	return &next
}

// CaptureRateTier caps the frame rate of sizes within LongSide x ShortSide
// after normalizing orientation; the 0x0 tier covers every larger size.
type CaptureRateTier struct {
	LongSide  int `json:"longSide"`
	ShortSide int `json:"shortSide"`
	FPS       int `json:"fps"`
}

// CaptureRateTiers is the native capture_rate.hpp table (a test compares
// them). 1088 is the legacy aligned FHD width; above QHD, 30 fps needs the
// video overclock.
var CaptureRateTiers = []CaptureRateTier{
	{LongSide: 1280, ShortSide: 720, FPS: 120},
	{LongSide: 1920, ShortSide: 1088, FPS: 75},
	{LongSide: 2560, ShortSide: 1440, FPS: 50},
	{FPS: 30},
}

// CaptureRateLimit is the frame-rate cap of a size. Input limits still apply
// when the output is downscaled; Auto retains the saved request.
func CaptureRateLimit(width, height int) int {
	if width <= 0 || height <= 0 {
		return 120
	}
	longer, shorter := max(width, height), min(width, height)
	for _, tier := range CaptureRateTiers {
		if tier.LongSide == 0 || (longer <= tier.LongSide && shorter <= tier.ShortSide) {
			return tier.FPS
		}
	}
	return CaptureRateTiers[0].FPS
}
