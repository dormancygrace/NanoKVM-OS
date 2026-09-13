package common

import (
	"os"
	"strconv"
	"strings"
	"sync"
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
func MonitorProfileSupported() bool {
	board, _ := os.ReadFile("/etc/kvm/hw")
	chip, _ := os.ReadFile("/etc/kvm/hdmi_version")
	return strings.TrimSpace(string(board)) == "pcie" && strings.TrimSpace(string(chip)) == "ux"
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
	wide := sourceWidth > 1920 || sourceHeight > 1080
	qhdOutput := next.Width == 2560 && next.Height == 1440
	qhdAuto := next.Width == 0 && next.Height == 0 &&
		sourceWidth == 2560 && sourceHeight == 1440
	qhd60 := os.Getenv("NANOKVM_QHD60_MAX_EXPERIMENT") == "1" &&
		(qhdOutput || qhdAuto)
	if wide && next.FPS > 30 {
		if qhd60 && next.FPS > 60 {
			next.FPS = 60
		} else if !qhd60 {
			next.FPS = 30
		}
	}
	return &next
}
