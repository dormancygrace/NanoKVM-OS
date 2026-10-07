//go:build teststub && cgo

package common

import (
	"bytes"
	"testing"
	"time"
)

func newTestCaptureWorker(t *testing.T) *videoCaptureWorker {
	t.Helper()
	worker, err := newVideoCaptureWorker()
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestCaptureWorkerAssemblesAccessUnits(t *testing.T) {
	worker := newTestCaptureWorker(t)
	defer worker.close()

	// H.265 IRAP: VPS, SPS, PPS and slice arrive as separate borrowed packs.
	packs := [][]byte{{0, 0, 0, 1, 0x40}, {0, 0, 0, 1, 0x42, 1}, {0, 0, 0, 1, 0x44}, {0, 0, 1, 0x26, 9, 8, 7}}
	fakeCaptureScript(packs, 3, 0, 0)
	state := &videoPackStorage{headroom: 9}
	code, err := worker.readVideo(2560, 1440, 2, 3000, 60, 60, state)
	if err != nil {
		t.Fatal(err)
	}
	// The fake vendor overwrites each pack once its sink returns, so equal
	// bytes show that every pack was copied while still borrowed.
	storage, data, result := state.videoResult(code)
	if result != 3 || !bytes.Equal(data, bytes.Join(packs, nil)) || len(storage) != 9+len(data) {
		t.Fatalf("key frame: result %d data %x", result, data)
	}
	if !bytes.Equal(storage[:9], make([]byte, 9)) {
		t.Fatal("headroom overwritten")
	}
	if args, _ := fakeCaptureLast(); args != [6]uint16{2560, 1440, 2, 3000, 60, 60} {
		t.Fatalf("parameters %v", args)
	}

	// Encoder parameters are taken from each request.
	fakeCaptureScript([][]byte{{0, 0, 1, 2, 5}}, 4, 0, 0)
	state = &videoPackStorage{}
	code, err = worker.readVideo(1920, 1080, 1, 2000, 30, 30, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, data, result = state.videoResult(code); result != 4 || !bytes.Equal(data, []byte{0, 0, 1, 2, 5}) {
		t.Fatalf("delta frame: result %d data %x", result, data)
	}
	if args, _ := fakeCaptureLast(); args != [6]uint16{1920, 1080, 1, 2000, 30, 30} {
		t.Fatalf("parameters %v", args)
	}
}

func TestCaptureWorkerKeepsResultCodes(t *testing.T) {
	worker := newTestCaptureWorker(t)
	defer worker.close()

	for _, test := range []struct {
		name       string
		packs      [][]byte
		native     int
		totalSkew  uint32
		offsetSkew int
		want, sink int
	}{
		{"native error", nil, -4, 0, 0, -4, 0},
		{"incomplete", [][]byte{{1, 2}}, 4, 1, 0, -2, 0},
		{"rejected pack", [][]byte{{1}, {2}}, 4, 0, 1, -3, -1},
	} {
		fakeCaptureScript(test.packs, test.native, test.totalSkew, test.offsetSkew)
		state := &videoPackStorage{}
		code, err := worker.readVideo(1280, 720, 2, 1000, 30, 30, state)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		storage, data, result := state.videoResult(code)
		if _, sink := fakeCaptureLast(); result != test.want || sink != test.sink || storage != nil || data != nil {
			t.Fatalf("%s: result %d sink %d", test.name, result, sink)
		}
	}
}

func TestCaptureWorkerReadsMjpeg(t *testing.T) {
	worker := newTestCaptureWorker(t)
	defer worker.close()

	fakeCaptureScript([][]byte{{0xff, 0xd8, 0xff, 0xd9}}, 0, 0, 0)
	state := &videoPackStorage{}
	code, err := worker.readMjpeg(1920, 1080, 80, state)
	if err != nil {
		t.Fatal(err)
	}
	if data, result := state.mjpegResult(code); result != 0 || !bytes.Equal(data, []byte{0xff, 0xd8, 0xff, 0xd9}) {
		t.Fatalf("result %d data %x", result, data)
	}
	if args, _ := fakeCaptureLast(); args[0] != 1920 || args[1] != 1080 || args[3] != 80 {
		t.Fatalf("parameters %v", args)
	}

	// Frame detection reports an unchanged image without a pack.
	fakeCaptureScript(nil, 5, 0, 0)
	state = &videoPackStorage{}
	if code, err = worker.readMjpeg(1920, 1080, 80, state); err != nil {
		t.Fatal(err)
	}
	if data, result := state.mjpegResult(code); result != 5 || data != nil {
		t.Fatalf("unchanged: result %d", result)
	}
}

func TestCaptureWorkerRejectsSecondRequest(t *testing.T) {
	worker := newTestCaptureWorker(t)
	defer worker.close()

	fakeCaptureScript([][]byte{{1}}, 4, 0, 0)
	if err := worker.submitVideo(640, 480, 2, 1000, 30, 30); err != nil {
		t.Fatal(err)
	}
	if err := worker.submitVideo(640, 480, 2, 1000, 30, 30); err == nil {
		t.Fatal("accepted a second outstanding request")
	}
	state := &videoPackStorage{}
	code, err := worker.collect(state)
	if _, data, result := state.videoResult(code); err != nil || result != 4 || !bytes.Equal(data, []byte{1}) {
		t.Fatalf("result %d err %v", result, err)
	}
}

// Close must not wait for a consumer that will never take the borrowed pack.
func TestCaptureWorkerCloseAbortsBorrowedPack(t *testing.T) {
	worker := newTestCaptureWorker(t)
	fakeCaptureScript([][]byte{{1, 2, 3}}, 4, 0, 0)
	if err := worker.submitVideo(640, 480, 2, 1000, 30, 30); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if _, err := worker.ready.Read(signal[:]); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		worker.close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close blocked on a borrowed pack")
	}
	if _, sink := fakeCaptureLast(); sink != -1 {
		t.Fatalf("aborted pack sink result %d", sink)
	}
}
