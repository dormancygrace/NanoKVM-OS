package videogate

import (
	"context"
	"testing"
	"time"
)

// A video that starts cancels the background work and waits until it has
// ended; no work starts while the video runs.
func TestHoldCancelsAndWaits(t *testing.T) {
	ctx, end, ok := Begin(context.Background())
	if !ok {
		t.Fatal("work refused with no video")
	}
	if _, _, ok := Begin(context.Background()); ok {
		t.Fatal("two background jobs at once")
	}
	ended := make(chan time.Time, 1)
	go func() {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // the helper exits
		ended <- time.Now()
		end()
	}()
	release := Hold()
	held := time.Now()
	if at := <-ended; held.Before(at) {
		t.Fatal("the video went on before the work ended")
	}
	if _, _, ok := Begin(context.Background()); ok {
		t.Fatal("work started beside video")
	}
	release()
	release()
	_, end, ok = Begin(context.Background())
	if !ok {
		t.Fatal("work refused after the video ended")
	}
	end()
	end()
}

func TestHoldDoesNotWaitForever(t *testing.T) {
	yieldWait = 20 * time.Millisecond
	defer func() { yieldWait = 5 * time.Second }()
	_, end, _ := Begin(context.Background())
	defer end()
	start := time.Now()
	release := Hold()
	defer release()
	if time.Since(start) > time.Second {
		t.Fatal("a stuck job held the video")
	}
}
