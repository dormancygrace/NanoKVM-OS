// Package videogate lets background work on the TPU give way to video. The
// OCR's models take the memory a viewer's video needs, so whatever wakes the
// capture or starts an encoder for a viewer holds the gate first (Hold): it
// cancels the background work and does not return until that work has
// ended, and no background work starts while a hold is on (Begin).
package videogate

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// yieldWait is how long a hold waits for cancelled background work before
// it logs that the video still waits; it then goes on waiting.
var yieldWait = 5 * time.Second

var (
	mu     sync.Mutex
	holds  int
	cancel context.CancelFunc
	done   chan struct{}
)

// Hold is taken before video wakes the capture or allocates. It cancels the
// background work running and returns only once that work has ended, so no
// video allocation runs beside it; ok is false when stop closed first (the
// video is no longer wanted; nil never closes). New work cannot start from
// the call until the release, which is safe to call more than once.
func Hold(stop <-chan struct{}) (release func(), ok bool) {
	mu.Lock()
	holds++
	mu.Unlock()
	var once sync.Once
	release = func() {
		once.Do(func() {
			mu.Lock()
			holds--
			mu.Unlock()
		})
	}
	for waited := time.Duration(0); ; waited += yieldWait {
		mu.Lock()
		stopWork, ended := cancel, done
		mu.Unlock()
		if stopWork == nil {
			return release, true
		}
		stopWork()
		timer := time.NewTimer(yieldWait)
		select {
		case <-ended:
			timer.Stop()
			return release, true
		case <-stop:
			timer.Stop()
			release()
			return nil, false
		case <-timer.C:
			log.Warnf("video waits for background TPU work to end (%v)", waited+yieldWait)
		}
	}
}

// Begin starts background work. Its context is cancelled when video holds
// the gate; end must be called once the work and its resources are gone. ok
// is false while video holds it or other background work runs.
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
