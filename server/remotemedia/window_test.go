package remotemedia

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func readStream(t *testing.T, read ReadBlock, image []byte, from, to int) {
	for ; from < to; from += 100 << 10 { // does not divide 1 MiB: reads straddle blocks
		n := min(100<<10, len(image)-from)
		if data, err := read(uint64(from), uint32(n)); err != nil || !bytes.Equal(data, image[from:from+n]) {
			t.Fatalf("read at %d: %v", from, err)
		}
	}
}

// The fake fetch of both sources rejects overlapping fetches, as the SMB reader
// requires, and stalls or fails one offset; zero is never read here.
func TestWindowPrefetch(t *testing.T) {
	image := httpImage()
	const step, mib = 100 << 10, 1 << 20
	var active, stall, fail atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	w := newWindow(ctx, func(ctx context.Context, _ time.Time, offset uint64, p []byte) error {
		if active.Add(1) != 1 {
			t.Error("overlapping fetches")
		}
		defer active.Add(-1)
		if int64(offset) == stall.Load() {
			<-ctx.Done()
			return ctx.Err()
		}
		if int64(offset) == fail.Load() {
			return errors.New("prefetch failed")
		}
		copy(p, image[offset:])
		return nil
	})
	w.size = uint64(len(image))
	readStream(t, w.read, image, step, len(image))
	// A seek joins the stalled prefetch of the third read before it fetches.
	stall.Store(4 * step)
	readStream(t, w.read, image, step, 4*step)
	readStream(t, w.read, image, 2*mib, 2*mib+step)
	fail.Store(mib + 3*step) // fails the read that needs it, not one before it
	readStream(t, w.read, image, mib, mib+3*step)
	readStream(t, w.read, image, mib+2*step, mib+3*step)
	if _, err := w.read(mib+3*step, step); err == nil {
		t.Fatal("prefetch error lost")
	}
	readStream(t, w.read, image, step, 4*step)
	cancel()
	w.close()
	if active.Load() != 0 || w.ahead != nil {
		t.Fatal("prefetch still running after close")
	}
}
