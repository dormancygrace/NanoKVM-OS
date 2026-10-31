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
	release, ok := Hold(nil)
	held := time.Now()
	if at := <-ended; !ok || held.Before(at) {
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

// Work that ends after the wait is still waited for: the hold never returns
// beside it, and work cannot start while the video waits.
func TestHoldWaitsPastTheDeadline(t *testing.T) {
	yieldWait = 20 * time.Millisecond
	defer func() { yieldWait = 5 * time.Second }()
	ctx, end, _ := Begin(context.Background())
	var ended time.Time
	go func() {
		<-ctx.Done()
		time.Sleep(5 * yieldWait) // slow driver cleanup
		ended = time.Now()
		end()
	}()
	release, ok := Hold(nil)
	if !ok || ended.IsZero() || time.Now().Before(ended) {
		t.Fatal("the hold returned before the work ended")
	}
	release()
}

// A video no longer wanted stops waiting, without a hold left behind.
func TestHoldGivesUpWhenStopped(t *testing.T) {
	yieldWait = 20 * time.Millisecond
	defer func() { yieldWait = 5 * time.Second }()
	_, end, _ := Begin(context.Background())
	stop := make(chan struct{})
	time.AfterFunc(30*time.Millisecond, func() { close(stop) })
	if release, ok := Hold(stop); ok || release != nil {
		t.Fatal("admitted beside running work")
	}
	end()
	_, end, ok := Begin(context.Background())
	if !ok {
		t.Fatal("a given-up hold stayed")
	}
	end()
}
