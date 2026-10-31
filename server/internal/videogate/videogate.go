// Package videogate lets background work on the TPU give way to video. The
// OCR's models take the memory a viewer's video needs, so a video session
// holds the gate while it runs (Hold): it cancels the background work and
// waits for it to end before the session allocates anything, and no
// background work starts while a hold is on (Begin).
package videogate

import (
	"context"
	"sync"
	"time"
)

// yieldWait bounds how long a starting video waits for cancelled background
// work: a killed helper process exits and frees its memory well within it.
var yieldWait = 5 * time.Second

var (
	mu     sync.Mutex
	holds  int
	cancel context.CancelFunc
	done   chan struct{}
)

// Hold is taken by a video session before it allocates. It cancels the
// background work running, waits for it to end (at most yieldWait), and keeps
// new work from starting until the release, which is safe to call more than
// once.
func Hold() func() {
	mu.Lock()
	holds++
	stop, ended := cancel, done
	mu.Unlock()
	if stop != nil {
		stop()
		timer := time.NewTimer(yieldWait)
		select {
		case <-ended:
		case <-timer.C:
		}
		timer.Stop()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			holds--
			mu.Unlock()
		})
	}
}

// Begin starts background work. Its context is cancelled when a video
// session starts; end must be called when the work and its resources are
// gone. ok is false while video runs or other background work does.
func Begin(parent context.Context) (ctx context.Context, end func(), ok bool) {
	mu.Lock()
	defer mu.Unlock()
	if holds > 0 || cancel != nil {
		return nil, nil, false
	}
	ctx, stop := context.WithCancel(parent)
	ended := make(chan struct{})
	cancel, done = stop, ended
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			mu.Lock()
			if done == ended {
				cancel, done = nil, nil
			}
			mu.Unlock()
			stop()
			close(ended)
		})
	}, true
}
