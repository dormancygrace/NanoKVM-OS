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
	wide    bool
}

// Cache reads away from the frame hot path. Native code independently enforces
// the same QHD cap immediately under its capture mutex.
func GetCaptureScreen() *Screen {
	next := *GetScreen()
	sourceTiming.Lock()
	if time.Now().After(sourceTiming.expires) {
		sourceTiming.wide = ReadVideoValue("/run/nanokvm/width") > 1920 || ReadVideoValue("/run/nanokvm/height") > 1080
		sourceTiming.expires = time.Now().Add(time.Second)
	}
	wide := sourceTiming.wide
	sourceTiming.Unlock()
	if wide && next.FPS > 30 {
		next.FPS = 30
	}
	return &next
}
