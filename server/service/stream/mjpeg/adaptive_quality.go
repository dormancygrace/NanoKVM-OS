package mjpeg

import "time"

// MJPEG sends every frame whole: at 2560x1440 with motion a JPEG is around
// 2 MB, more than Wi-Fi or the TLS encryption of the server carry at the
// capture rate, and the stream turned into a slide show at a fixed quality.
// adaptiveQuality sets the hardware JPEG quality from what the viewers take:
// while they are still busy with earlier frames when the next capture is due
// it steps the quality down, and while they keep up it climbs back to the
// configured quality. A still screen (identical frames are not sent) climbs
// to the configured quality, so text stays sharp.
const (
	minAdaptiveQuality = 30
	qualityWindow      = 500 * time.Millisecond
	// The share of capture ticks with every viewer busy above which the
	// quality steps down. Below it and above zero the quality holds.
	busyShareLimit = 0.25
)

type adaptiveQuality struct {
	quality int
	window  time.Time
	ticks   int
	busy    int
}

// next records one capture tick, busy when no viewer could take a frame,
// and returns the quality for the next capture, at most ceiling.
func (a *adaptiveQuality) next(ceiling int, busy bool, now time.Time) int {
	if a.quality == 0 || a.quality > ceiling {
		a.quality = ceiling
	}
	if a.window.IsZero() {
		a.window = now
	}
	a.ticks++
	if busy {
		a.busy++
	}
	if now.Sub(a.window) < qualityWindow {
		return a.quality
	}
	share := float64(a.busy) / float64(a.ticks)
	switch {
	case share > busyShareLimit:
		a.quality -= max(5, a.quality/5)
	case a.busy == 0:
		a.quality += 5
	}
	a.quality = min(max(a.quality, min(minAdaptiveQuality, ceiling)), ceiling)
	a.window, a.ticks, a.busy = now, 0, 0
	return a.quality
}
