package common

import (
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// MaxQualityValue is the largest accepted quality value: MJPEG quality is
// 1-100, larger values are H.26x bitrates in kbit/s.
const MaxQualityValue = 20000

type Screen struct {
	Width       uint16
	Height      uint16
	FPS         int
	Quality     uint16
	BitRate     uint16
	GOP         uint8
	GOPMode     uint8
	MjpegChroma uint16
}

const (
	GOPModeNormalP uint8 = iota
	GOPModeSmartP
)

var (
	screen      atomic.Pointer[Screen]
	screenOnce  sync.Once
	screenMutex sync.Mutex
)

var screenFileMap = map[string]string{
	"fps":          "/kvmapp/kvm/fps",
	"gop_mode":     "/kvmapp/kvm/gop_mode",
	"mjpeg_chroma": "/kvmapp/kvm/mjpeg_chroma",
	"quality":      "/kvmapp/kvm/qlty",
	"resolution":   "/kvmapp/kvm/res",
}

// ResolutionMap height to width
var ResolutionMap = map[uint16]uint16{
	2160: 3840,
	1440: 2560,
	1080: 1920,
	720:  1280,
	600:  800,
	0:    0,
}

var QualityMap = map[uint16]bool{
	100: true,
	80:  true,
	60:  true,
	50:  true,
}

var BitRateMap = map[uint16]bool{
	20000: true,
	15000: true,
	12000: true,
	10000: true,
	8000:  true,
	5000:  true,
	3000:  true,
	2000:  true,
	1000:  true,
}

// GetScreen returns an immutable snapshot. Call SetScreen to publish changes.
func GetScreen() *Screen {
	screenOnce.Do(func() {
		initial := loadScreen(os.ReadFile)
		if initial.Height > 1440 && !SupportsUHD() {
			initial.Width, initial.Height = 2560, 1440
		}
		if initial.Height > 1080 && !SupportsQHD() {
			initial.Width, initial.Height = 1920, 1080
		}
		screen.Store(initial)
	})

	return screen.Load()
}

func SetScreen(key string, value int) {
	GetScreen()
	screenMutex.Lock()
	defer screenMutex.Unlock()
	next := *screen.Load()
	setScreenValue(&next, key, value)
	screen.Store(&next)
}

func setScreenValue(target *Screen, key string, value int) {
	switch key {
	case "resolution":
		// 0 is "same as input", which ResolutionMap maps to 0x0.
		if value < 0 || value > math.MaxUint16 {
			return
		}
		height := uint16(value)
		if width, ok := ResolutionMap[height]; ok {
			target.Width = width
			target.Height = height
		}

	case "quality":
		// 1-100 is MJPEG quality, larger values are H.26x bitrates in kbit/s.
		if value >= 1 && value <= 100 {
			target.Quality = uint16(value)
		} else if value > 100 && value <= MaxQualityValue {
			target.BitRate = uint16(value)
		}

	case "mjpeg_chroma":
		switch value {
		case 420:
			target.MjpegChroma = 420
		case 422:
			target.MjpegChroma = 422
		}

	case "fps":
		target.FPS = validateFPS(value)

	case "gop":
		if value >= 1 && value <= 100 {
			target.GOP = uint8(value)
		}

	case "gop_mode":
		switch value {
		case int(GOPModeNormalP):
			target.GOPMode = GOPModeNormalP
		case int(GOPModeSmartP):
			target.GOPMode = GOPModeSmartP
		}
	}
}

// QHD includes retained JPEG/H26x encoders and a lazy copy buffer. Keep at
// least 2 MiB above their 59.914 MiB measured peak (see ION qualification).
// Read the booted Device Tree, independently of debugfs being mounted.
func SupportsQHD() bool {
	return ionAtLeast(62 * 1024 * 1024)
}

// SupportsUHD reports the 128 MiB video pool that 3840x2160 needs: 117 MiB
// with SmartP (see uhd_ion_mib in kvm_vision.cpp). It must be a fixed
// carveout: with CMA the encoder's UHD buffers failed on 2 of 4 cold boots,
// when Linux's borrowed pages could not be migrated back.
func SupportsUHD() bool {
	_, err := os.Stat("/proc/device-tree/reserved-memory/ion/reusable")
	return ionAtLeast(128*1024*1024) && os.IsNotExist(err)
}

func ionAtLeast(bytes uint32) bool {
	data, err := os.ReadFile("/proc/device-tree/reserved-memory/ion/size")
	return err == nil && len(data) == 4 && binary.BigEndian.Uint32(data) >= bytes
}

func CheckScreen() {
	GetScreen()
	screenMutex.Lock()
	defer screenMutex.Unlock()
	previous := screen.Load()
	next := *previous
	checkScreen(&next)
	if next != *previous {
		screen.Store(&next)
	}
}

func checkScreen(target *Screen) {
	if target.MjpegChroma != 420 && target.MjpegChroma != 422 {
		target.MjpegChroma = 422
	}
	if _, ok := ResolutionMap[target.Height]; !ok {
		target.Width = 1920
		target.Height = 1080
	}

	if _, ok := QualityMap[target.Quality]; !ok {
		target.Quality = 80
	}

	if _, ok := BitRateMap[target.BitRate]; !ok {
		target.BitRate = 12000
	}

	if target.GOPMode != GOPModeNormalP && target.GOPMode != GOPModeSmartP {
		target.GOPMode = GOPModeSmartP
	}
}

func loadScreen(readFile func(string) ([]byte, error)) *Screen {
	target := &Screen{
		Width:       0,
		Height:      0,
		Quality:     80,
		FPS:         100,
		BitRate:     12000, // recommended for 1080p100 H.265 with motion
		GOP:         30,
		GOPMode:     GOPModeSmartP,
		MjpegChroma: 422,
	}

	if os.Getenv("NANOKVM_MJPEG_422") == "0" {
		target.MjpegChroma = 420
	}

	for key, path := range screenFileMap {
		data, err := readFile(path)
		if err != nil {
			continue
		}

		value, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}

		setScreenValue(target, key, value)
	}

	checkScreen(target)
	return target
}

func validateFPS(fps int) int {
	if fps > 120 {
		return 120
	}
	if fps < 10 {
		return 10
	}

	return fps
}
