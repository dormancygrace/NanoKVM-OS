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
	counter      *FrameRateCounter
	mjpegCounter *FrameRateCounter
	counterOnce  sync.Once
)

// rateWindowSeconds is how many one-second counts a rate averages.
const rateWindowSeconds = 5

// rateWindow is a moving average over the last rateWindowSeconds counts.
type rateWindow struct {
	counts [rateWindowSeconds]int64
	next   int
	filled int
}

// add records one second's count and returns the rounded average per second.
func (w *rateWindow) add(count int64) int64 {
	w.counts[w.next] = count
	w.next = (w.next + 1) % rateWindowSeconds
	if w.filled < rateWindowSeconds {
		w.filled++
	}
	var sum int64
	for _, c := range w.counts[:w.filled] {
		sum += c
	}
	return (sum + int64(w.filled)/2) / int64(w.filled)
}

// FrameRateCounter measures a published stream: frames and bytes per second.
type FrameRateCounter struct {
	frameCount atomic.Int32
	fps        atomic.Int32
	bytes      atomic.Int64
}

// Every second, averaged over the last five: /run/nanokvm/mjpeg_fps is the
// MJPEG stream, now_fps the main stream (H.264/H.265 while it runs, else
// MJPEG, as the OLED and the dashboard show it) and now_kbps the
// H.264/H.265 bit rate.
func startFrameRateCounters() {
	counter = &FrameRateCounter{}
	mjpegCounter = &FrameRateCounter{}

	go func() {
		var videoFrames, mjpegFrames, videoBytes rateWindow
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for range ticker.C {
			video := videoFrames.add(int64(counter.frameCount.Swap(0)))
			mjpeg := mjpegFrames.add(int64(mjpegCounter.frameCount.Swap(0)))
			kbps := videoBytes.add(counter.bytes.Swap(0)) * 8 / 1000
			mjpegCounter.bytes.Store(0)
			counter.fps.Store(int32(video))
			mjpegCounter.fps.Store(int32(mjpeg))

			main := video
			if main == 0 {
				main = mjpeg
			}
			if err := os.WriteFile("/run/nanokvm/now_fps", []byte(fmt.Sprintf("%d", main)), 0o666); err != nil {
				log.Errorf("failed to write fps: %s", err)
			}
			_ = os.WriteFile("/run/nanokvm/mjpeg_fps", []byte(fmt.Sprintf("%d", mjpeg)), 0o666)
			_ = os.WriteFile("/run/nanokvm/now_kbps", []byte(fmt.Sprintf("%d", kbps)), 0o666)
		}
	}()
}

// GetFrameRateCounter is the H.264/H.265 stream's counter.
func GetFrameRateCounter() *FrameRateCounter {
	counterOnce.Do(startFrameRateCounters)
	return counter
}

// GetMjpegFrameRateCounter is the MJPEG stream's counter.
func GetMjpegFrameRateCounter() *FrameRateCounter {
	counterOnce.Do(startFrameRateCounters)
	return mjpegCounter
}

func (f *FrameRateCounter) Update() {
	f.frameCount.Add(1)
}

// AddBytes counts the size of a published frame.
func (f *FrameRateCounter) AddBytes(n int) {
	f.bytes.Add(int64(n))
}
