//go:build teststub && cgo

package common

import (
	"bytes"
	"testing"
	"time"
)

var testCaptureParams = VideoCaptureParams{Width: 2560, Height: 1440, Codec: 2, BitRate: 3000, FPS: 120}

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
	if reads, _ := fakeCaptureReads(); reads[0].args != [5]uint16{2560, 1440, 2, 3000, 120} {
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
