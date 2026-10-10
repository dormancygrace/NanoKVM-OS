//go:build teststub && cgo

package common

import (
	"bytes"
	"testing"
	"time"
)

var testCaptureParams = VideoCaptureParams{Width: 2560, Height: 1440, Codec: 2, BitRate: 3000, GOP: 60, FPS: 120}

// The worker paces a fake native reader; block makes an empty queue wait like
// a VPSS frame wait.
func startTestCapture(t *testing.T, block bool, params VideoCaptureParams) (*videoCaptureWorker, *VideoCapture) {
	t.Helper()
	fakeCaptureReset(block)
	worker, err := newVideoCaptureWorker()
	if err != nil {
		t.Fatal(err)
	}
	capture := worker.start(params, nil)
	t.Cleanup(func() {
		fakeCaptureUnblock()
		capture.Close()
		worker.close()
	})
	return worker, capture
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(time.Millisecond)
	}
}

func readerWaiting() bool {
	_, waiting := fakeCaptureReads()
	return waiting
}

func readCount() int {
	reads, _ := fakeCaptureReads()
	return len(reads)
}

func TestCaptureAdvanceMatchesGoCadence(t *testing.T) {
	// The cases of TestCaptureDeadlinePreservesCadenceAndBoundsCatchup.
	period := time.Second / 75
	for _, tc := range []struct {
		name      string
		now, want time.Duration
	}{
		{"on time", 0, period},
		{"small delay retains phase", 2 * time.Millisecond, period},
		{"one missed period catches up", period + time.Millisecond, period},
		{"long stall drops stale work", 10 * period, 10 * period},
	} {
		start := time.Hour
		if got := captureAdvance(start, start+tc.now, period); got != start+tc.want {
			t.Errorf("%s: deadline=%v want=%v", tc.name, got-start, tc.want)
		}
	}
}

func TestVideoCaptureAssemblesAccessUnits(t *testing.T) {
	_, capture := startTestCapture(t, true, testCaptureParams)
	// H.265 IRAP: VPS, SPS, PPS and slice arrive as separate borrowed packs.
	packs := [][]byte{{0, 0, 0, 1, 0x40}, {0, 0, 0, 1, 0x42, 1}, {0, 0, 0, 1, 0x44}, {0, 0, 1, 0x26, 9, 8, 7}}
	fakeCapturePush(packs, 3, 0, 0)
	fakeCapturePush([][]byte{{0, 0, 1, 2, 5}}, 4, 0, 0)

	// The fake vendor overwrites each pack once its sink returns, so equal
	// bytes show that every pack was copied while still borrowed.
	storage, data, result, ok := capture.Next(9)
	if !ok || result != 3 || !bytes.Equal(data, bytes.Join(packs, nil)) || len(storage) != 9+len(data) {
		t.Fatalf("key frame: ok %t result %d data %x", ok, result, data)
	}
	if !bytes.Equal(storage[:9], make([]byte, 9)) {
		t.Fatal("headroom overwritten")
	}
	if _, data, result, ok = capture.Next(0); !ok || result != 4 || !bytes.Equal(data, []byte{0, 0, 1, 2, 5}) {
		t.Fatalf("delta frame: ok %t result %d data %x", ok, result, data)
	}
	if reads, _ := fakeCaptureReads(); reads[0].args != [6]uint16{2560, 1440, 2, 3000, 60, 120} {
		t.Fatalf("parameters %v", reads[0].args)
	}
}

func TestVideoCaptureKeepsResultCodes(t *testing.T) {
	_, capture := startTestCapture(t, true, testCaptureParams)
	fakeCapturePush(nil, -4, 0, 0)
	fakeCapturePush([][]byte{{1, 2}}, 4, 1, 0)   // incomplete
	fakeCapturePush([][]byte{{1}, {2}}, 4, 0, 1) // rejected second pack
	fakeCapturePush([][]byte{{7}}, 4, 0, 0)
	for _, want := range []int{-4, -2, -3, 4} {
		storage, data, result, ok := capture.Next(9)
		if !ok || result != want || (result < 0) != (storage == nil && data == nil) {
			t.Fatalf("result %d want %d", result, want)
		}
	}
}

func TestVideoCapturePacesReadsAtRequestedRate(t *testing.T) {
	params := testCaptureParams
	params.FPS = 50
	_, capture := startTestCapture(t, false, params)
	for range 6 {
		fakeCapturePush([][]byte{{1}}, 4, 0, 0)
	}
	for range 6 {
		if _, _, result, ok := capture.Next(0); !ok || result != 4 {
			t.Fatalf("result %d", result)
		}
	}
	reads, _ := fakeCaptureReads()
	// Absolute cadence: no burst, and scheduling delays do not accumulate.
	if elapsed := reads[5].at - reads[0].at; elapsed < 90*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("five periods of 20 ms took %v", elapsed)
	}
}

func TestVideoCaptureUpdateAppliesToNextRead(t *testing.T) {
	_, capture := startTestCapture(t, true, testCaptureParams)
	fakeCapturePush([][]byte{{1}}, 3, 0, 0)
	if _, _, _, ok := capture.Next(0); !ok {
		t.Fatal("first frame")
	}
	// The thread already waits in its next read with the old parameters.
	waitUntil(t, "read ahead", readerWaiting)
	updated := testCaptureParams
	updated.Width, updated.Height, updated.BitRate, updated.GOP = 1920, 1080, 2000, 30
	capture.Update(updated)
	fakeCapturePush([][]byte{{2}}, 4, 0, 0)
	fakeCapturePush([][]byte{{3}}, 4, 0, 0)
	for range 2 {
		if _, _, _, ok := capture.Next(0); !ok {
			t.Fatal("frame")
		}
	}
	reads, _ := fakeCaptureReads()
	if reads[1].args != [6]uint16{2560, 1440, 2, 3000, 60, 120} || reads[2].args != [6]uint16{1920, 1080, 2, 2000, 30, 120} {
		t.Fatalf("parameters %v %v", reads[1].args, reads[2].args)
	}
}

func TestVideoCaptureStopInterruptsAndCloseWaitsForRead(t *testing.T) {
	worker, capture := startTestCapture(t, true, testCaptureParams)
	next := make(chan bool, 1)
	go func() {
		_, _, _, ok := capture.Next(0)
		next <- ok
	}()
	waitUntil(t, "native read", readerWaiting)
	capture.Stop()
	select {
	case ok := <-next:
		if ok {
			t.Fatal("Next delivered after Stop")
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not interrupt Next")
	}
	if capture.Err() != nil {
		t.Fatal(capture.Err())
	}

	closed := make(chan struct{})
	go func() {
		capture.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a native read was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	fakeCaptureUnblock()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the read")
	}

	// The aborted read's slot was dropped; a new stream starts clean.
	fakeCaptureReset(true)
	next2 := worker.start(testCaptureParams, nil)
	defer next2.Close()
	fakeCapturePush([][]byte{{9}}, 3, 0, 0)
	if _, data, result, ok := next2.Next(0); !ok || result != 3 || !bytes.Equal(data, []byte{9}) {
		t.Fatalf("restart: result %d data %x", result, data)
	}
}

func TestVideoCapturePauseExcludesReads(t *testing.T) {
	worker, capture := startTestCapture(t, true, testCaptureParams)
	waitUntil(t, "native read", readerWaiting)
	paused := make(chan struct{})
	go func() {
		worker.pause()
		close(paused)
	}()
	select {
	case <-paused:
		t.Fatal("pause returned while a native read was in flight")
	case <-time.After(30 * time.Millisecond):
	}
	fakeCapturePush([][]byte{{1}}, 3, 0, 0)
	<-paused
	reads := readCount()
	fakeCapturePush([][]byte{{2}}, 4, 0, 0)
	time.Sleep(50 * time.Millisecond)
	if readCount() != reads {
		t.Fatal("read started while paused")
	}
	worker.resume()
	for _, want := range []byte{1, 2} {
		if _, data, _, ok := capture.Next(0); !ok || !bytes.Equal(data, []byte{want}) {
			t.Fatalf("frame %d: %x", want, data)
		}
	}
}

func TestVideoCaptureWakesConsumerOncePerFrame(t *testing.T) {
	worker, capture := startTestCapture(t, true, testCaptureParams)
	const frames = 5
	for i := range frames {
		waitUntil(t, "native read", readerWaiting)
		go func() {
			time.Sleep(5 * time.Millisecond)
			fakeCapturePush([][]byte{{byte(i)}}, 4, 0, 0)
		}()
		if _, data, _, ok := capture.Next(0); !ok || !bytes.Equal(data, []byte{byte(i)}) {
			t.Fatalf("frame %d: %x", i, data)
		}
	}
	stats := worker.stats()
	if stats.frames != frames || stats.signals > frames || stats.waits > frames || stats.rings != 0 {
		t.Fatalf("handoffs %+v", stats)
	}
}

// Both slots full: the thread waits, and taking one slot wakes it once.
func TestVideoCaptureThrottlesWhenBothSlotsAreFull(t *testing.T) {
	worker, capture := startTestCapture(t, false, testCaptureParams)
	for i := range 3 {
		fakeCapturePush([][]byte{{byte(i)}}, 4, 0, 0)
	}
	waitUntil(t, "both slots", func() bool { return worker.stats().producerWaits > 0 })
	if reads := readCount(); reads != 2 {
		t.Fatalf("%d reads with two slots", reads)
	}
	if _, data, _, ok := capture.Next(0); !ok || !bytes.Equal(data, []byte{0}) {
		t.Fatalf("first frame %x", data)
	}
	waitUntil(t, "third read", func() bool { return readCount() >= 3 })
	if rings := worker.stats().rings; rings != 1 {
		t.Fatalf("%d producer rings", rings)
	}
	for _, want := range []byte{1, 2} {
		if _, data, _, ok := capture.Next(0); !ok || !bytes.Equal(data, []byte{want}) {
			t.Fatalf("frame %d: %x", want, data)
		}
	}
}
