// Package attention tells whether somebody is actually looking at the video:
// an open tab alone is not enough, since a tab left open in the background or
// on a screen nobody looks at keeps its video stream. Somebody watches while
// a browser page with input is visible and keyboard or mouse input came from
// a page recently.
package attention

import (
	"sync/atomic"
	"time"
)

var (
	visiblePages atomic.Int64
	lastInput    atomic.Int64 // Unix nanoseconds; 0 before any input
)

// PageShown and PageHidden count the visible pages of the input sockets.
func PageShown()  { visiblePages.Add(1) }
func PageHidden() { visiblePages.Add(-1) }

// NoteInput records keyboard or mouse input from a page.
func NoteInput(now time.Time) { lastInput.Store(now.UnixNano()) }

// Watching reports a visible page and input within the last `within`.
func Watching(now time.Time, within time.Duration) bool {
	last := lastInput.Load()
	return visiblePages.Load() > 0 && last != 0 && now.Sub(time.Unix(0, last)) < within
}
