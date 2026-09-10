package common

import (
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type Screen struct {
	Width   uint16
	Height  uint16
	FPS     int
	Quality uint16
	BitRate uint16
	GOP     uint8
}

var (
	screen      atomic.Pointer[Screen]
	screenOnce  sync.Once
	screenMutex sync.Mutex
)

var screenFileMap = map[string]string{
	"fps":        "/kvmapp/kvm/fps",
	"quality":    "/kvmapp/kvm/qlty",
	"resolution": "/kvmapp/kvm/res",
}

// ResolutionMap height to width
var ResolutionMap = map[uint16]uint16{
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
	10000: true,
	5000:  true,
	3000:  true,
	2000:  true,
	1000:  true,
}

// GetScreen returns an immutable snapshot. Call SetScreen to publish changes.
func GetScreen() *Screen {
	screenOnce.Do(func() {
		initial := loadScreen(os.ReadFile)
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
		height := uint16(value)
		if width, ok := ResolutionMap[height]; ok {
			target.Width = width
			target.Height = height
		}

	case "quality":
		if value > 100 {
			target.BitRate = uint16(value)
		} else {
			target.Quality = uint16(value)
		}

	case "fps":
		target.FPS = validateFPS(value)

	case "gop":
		target.GOP = uint8(value)
	}
}

// QHD includes retained JPEG/H26x encoders and a lazy copy buffer. Keep at
// least 2 MiB above their 59.914 MiB measured peak (see ION qualification).
// Read the booted Device Tree, independently of debugfs being mounted.
func SupportsQHD() bool {
	data, err := os.ReadFile("/proc/device-tree/reserved-memory/ion/size")
	return err == nil && len(data) == 4 && binary.BigEndian.Uint32(data) >= 62*1024*1024
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
	if _, ok := ResolutionMap[target.Height]; !ok {
		target.Width = 1920
		target.Height = 1080
	}

	if _, ok := QualityMap[target.Quality]; !ok {
		target.Quality = 80
	}

	if _, ok := BitRateMap[target.BitRate]; !ok {
		target.BitRate = 3000
	}
}

func loadScreen(readFile func(string) ([]byte, error)) *Screen {
	target := &Screen{
		Width:   0,
		Height:  0,
		Quality: 80,
		FPS:     30,
		BitRate: 3000,
		GOP:     30,
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
	if fps > 60 {
		return 60
	}
	if fps < 10 {
		return 10
	}

	return fps
}
