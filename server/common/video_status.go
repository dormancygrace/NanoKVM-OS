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

// Board and HDMI chip identifiers; variables so tests can select a board.
var (
	monitorBoardFile    = "/etc/kvm/hw"
	monitorHDMIChipFile = "/etc/kvm/hdmi_version"
)

// The board decides how a new EDID takes effect: PCIe boards reset the
// receiver, Cube and Lite boards (alpha, beta) need a power cycle. The
// receiver decides what the monitor may advertise: the LT6911UXC takes every
// profile, the LT6911C and LT6911D take 720p and 1080p at 60 Hz until they are
// measured beyond that. nanokvm_update_edid programs all three.
func monitorBoard() string {
	return hardwareIdentity(monitorBoardFile, "alpha", "beta", "pcie")
}
func receiverChip() string {
	return hardwareIdentity(monitorHDMIChipFile, "c", "ux", "d", "ue")
}

// S15kvmhwd publishes both before the server starts and keeps a known value
// (only an explicit re-detect rewrites it), so a known value is read once per
// file: the capability answer alone asks about 60 times, and a Go file read
// costs nine system calls. Anything else is read again next time.
var hardwareIdentities sync.Map

func hardwareIdentity(path string, known ...string) string {
	if value, ok := hardwareIdentities.Load(path); ok {
		return value.(string)
	}
	data, _ := os.ReadFile(path)
	value := strings.TrimSpace(string(data))
	for _, k := range known {
		if value == k {
			hardwareIdentities.Store(path, value)
			break
		}
	}
	return value
}

// MonitorProfileSupported describes EDID programming, not capture timings.
func MonitorProfileSupported() bool {
	return monitorHardwareSupported(monitorBoard(), receiverChip())
}
func monitorHardwareSupported(board, chip string) bool {
	switch board {
	case "pcie":
		return chip == "ux" || chip == "d"
	case "alpha", "beta":
		return chip == "c" || chip == "ux" || chip == "d"
	}
	return false
}

// HDMIReceiverModel names the HDMI receiver S15kvmhwd identified, or "".
func HDMIReceiverModel() string {
	return hdmiReceiverModel(receiverChip())
}
func hdmiReceiverModel(chip string) string {
	switch chip {
	case "c":
		return "LT6911C"
	case "ux":
		return "LT6911UXC"
	case "d":
		return "LT6911D"
	}
	return ""
}
func MonitorRequiresPowerCycle() bool {
	board := monitorBoard()
	return board == "alpha" || board == "beta"
}

// MonitorHighRefreshSupported reports a receiver that takes the full profile
// set (above 1080p60, QHD, UHD, portrait): the LT6911UXC, on any board.
func MonitorHighRefreshSupported() bool {
	return MonitorProfileSupported() && receiverChip() == "ux"
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
// them). With the video overclock the encoder sustains about 250 Mpix/s: QHD
// 60 and UHD 30 fps; 1080p gets 100 of the about 109 fps it reaches. 1088 is
// the legacy aligned FHD width. 2560x1600 runs at 50 (at 60 the capture
// delivered about 50 and H.265 fell to 15 fps), 3440x1440 at 40 (at 50 one
// frame takes the encoder a whole frame period and every second one is lost).
var CaptureRateTiers = []CaptureRateTier{
	{LongSide: 1280, ShortSide: 720, FPS: 120},
	{LongSide: 1920, ShortSide: 1088, FPS: 100},
	{LongSide: 2560, ShortSide: 1440, FPS: 60},
	{LongSide: 2560, ShortSide: 1600, FPS: 50},
	{LongSide: 3440, ShortSide: 1440, FPS: 40},
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
