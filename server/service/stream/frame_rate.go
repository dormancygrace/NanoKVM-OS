package stream

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	counter     *FrameRateCounter
	counterOnce sync.Once
)

// FrameRateCounter measures the published H.264/H.265 stream: frames and
// bits per second over three seconds, in /run/nanokvm/now_fps and now_kbps.
type FrameRateCounter struct {
	frameCount atomic.Int32
	fps        atomic.Int32
	bytes      atomic.Int64
}

func GetFrameRateCounter() *FrameRateCounter {
	counterOnce.Do(func() {
		counter = &FrameRateCounter{}

		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				currentCount := counter.frameCount.Swap(0)
				counter.fps.Store(currentCount / 3)

				data := fmt.Sprintf("%d", counter.fps.Load())
				err := os.WriteFile("/run/nanokvm/now_fps", []byte(data), 0o666)
				if err != nil {
					log.Errorf("failed to write fps: %s", err)
				}
				kbps := counter.bytes.Swap(0) * 8 / 3 / 1000
				_ = os.WriteFile("/run/nanokvm/now_kbps", []byte(fmt.Sprintf("%d", kbps)), 0o666)
			}
		}()
	})

	return counter
}

func (f *FrameRateCounter) Update() {
	f.frameCount.Add(1)
}

// AddBytes counts the size of a published frame.
func (f *FrameRateCounter) AddBytes(n int) {
	f.bytes.Add(int64(n))
}
