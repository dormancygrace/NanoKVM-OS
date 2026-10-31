package vm

import (
	"context"
	"reflect"
	"testing"
	"time"

	"NanoKVM-Server/internal/videogate"
	hdmistate "NanoKVM-Server/service/vm/hdmi_state"
)

type parkRecorder struct {
	calls   []bool
	result  bool
	pending bool
}

func (r *parkRecorder) snapshot() []bool {
	hdmiMutex.Lock()
	defer hdmiMutex.Unlock()
	return append([]bool{}, r.calls...)
}

func withParkHooks(t *testing.T) *parkRecorder {
	t.Helper()
	recorder := &parkRecorder{result: true}
	oldPending := captureReclaimPending
	captureReclaimPending = func() bool { return recorder.pending }
	oldPark, oldDisabled, oldTimeout, oldPersist, oldHDMI, oldDelay, oldReclaimDelay := setCaptureParked, isHdmiDisabled, getHdmiIdleTimeout, persistHdmiIdleTimeout, setHDMI, hdmiCaptureParkDelay, hdmiCaptureReclaimDelay
	setCaptureParked = func(parked bool) bool {
		recorder.calls = append(recorder.calls, parked)
		return recorder.result
	}
	isHdmiDisabled = func() bool { return false }
	getHdmiIdleTimeout = func() int { return 0 }
	persistHdmiIdleTimeout = func(int) {}
	setHDMI = func(bool) {}
	hdmiCaptureParkDelay = 20 * time.Millisecond
	// Individual retry tests shorten this; transition tests observe one park.
	hdmiCaptureReclaimDelay = time.Hour
	reset := func() {
		hdmiMutex.Lock()
		stopHdmiParkTimerLocked()
		stopHdmiIdleTimerLocked()
		hdmiIdleGeneration++
		hdmiDemand = hdmistate.New()
		hdmiCaptureParked = false
		hdmiStoppedForIdle = false
		hdmiMutex.Unlock()
		viewerGateMutex.Lock()
		if viewerGate != nil {
			viewerGate()
			viewerGate = nil
		}
		viewerGateMutex.Unlock()
	}
	reset()
	t.Cleanup(func() {
		reset()
		captureReclaimPending = oldPending
		setCaptureParked, isHdmiDisabled, getHdmiIdleTimeout, persistHdmiIdleTimeout, setHDMI, hdmiCaptureParkDelay, hdmiCaptureReclaimDelay = oldPark, oldDisabled, oldTimeout, oldPersist, oldHDMI, oldDelay, oldReclaimDelay
	})
	return recorder
}

// waitParkCalls polls until the calls match, for timers under -race on a
// loaded machine.
func waitParkCalls(t *testing.T, recorder *parkRecorder, want ...bool) {
	t.Helper()
	if want == nil {
		want = []bool{}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := recorder.snapshot()
		if reflect.DeepEqual(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("park calls %v, want %v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// expectNoMoreParkCalls waits past the park delay and checks that nothing
// else happened.
func expectNoMoreParkCalls(t *testing.T, recorder *parkRecorder, want ...bool) {
	t.Helper()
	time.Sleep(5 * hdmiCaptureParkDelay)
	waitParkCalls(t, recorder, want...)
}

func TestCaptureParksAfterLastViewerAndResumesForNext(t *testing.T) {
	recorder := withParkHooks(t)
	UpdateHdmiViewerSnapshot("webrtc", 1, 1)
	UpdateHdmiViewerSnapshot("webrtc", 0, 2)
	waitParkCalls(t, recorder, true)

	UpdateHdmiViewerSnapshot("direct", 1, 1)
	waitParkCalls(t, recorder, true, false)
	UpdateHdmiViewerSnapshot("direct", 2, 2)
	expectNoMoreParkCalls(t, recorder, true, false)
}

// A viewer that wakes a parked capture (which restores its VPSS pools)
// first waits for the background OCR it cancels to end; no background read
// starts while viewers are there, and one can once they have gone.
func TestViewerWakesParkedCaptureOnlyAfterOCREnded(t *testing.T) {
	recorder := withParkHooks(t)
	UpdateHdmiViewerSnapshot("webrtc", 1, 1)
	UpdateHdmiViewerSnapshot("webrtc", 0, 2)
	waitParkCalls(t, recorder, true)

	ctx, end, ok := videogate.Begin(context.Background())
	if !ok {
		t.Fatal("background read refused")
	}
	beforeEnd := make(chan []bool, 1)
	go func() {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // the helper exits
		beforeEnd <- recorder.snapshot()
		end()
	}()
	UpdateHdmiViewerSnapshot("direct", 1, 1)
	select {
	case calls := <-beforeEnd:
		if !reflect.DeepEqual(calls, []bool{true}) {
			t.Fatalf("the capture woke beside the OCR: %v", calls)
		}
	case <-time.After(5 * time.Second):
		end()
		t.Fatalf("the viewer did not cancel the OCR; park calls %v", recorder.snapshot())
	}
	waitParkCalls(t, recorder, true, false)
	if _, _, ok := videogate.Begin(context.Background()); ok {
		t.Fatal("a background read started beside a viewer")
	}
	UpdateHdmiViewerSnapshot("direct", 0, 2)
	_, end, ok = videogate.Begin(context.Background())
	if !ok {
		t.Fatal("the gate stayed held without viewers")
	}
	end()
}

func TestCaptureStaysUpWhenViewerReturnsWithinDelay(t *testing.T) {
	recorder := withParkHooks(t)
	hdmiCaptureParkDelay = time.Second
	UpdateHdmiViewerSnapshot("webrtc", 1, 1)
	UpdateHdmiViewerSnapshot("webrtc", 0, 2)
	UpdateHdmiViewerSnapshot("webrtc", 1, 3)
	time.Sleep(3 * time.Second / 2)
	waitParkCalls(t, recorder)
}

func TestScreenshotLeaseResumesParkedCapture(t *testing.T) {
	recorder := withParkHooks(t)
	UpdateHdmiViewerSnapshot("mjpeg", 0, 1)
	waitParkCalls(t, recorder, true)

	release := AcquireHdmiCaptureLease()
	waitParkCalls(t, recorder, true, false)
	release()
	waitParkCalls(t, recorder, true, false, true)
}

func TestFailedParkIsNotResumed(t *testing.T) {
	recorder := withParkHooks(t)
	recorder.result = false
	UpdateHdmiViewerSnapshot("webrtc", 0, 1)
	waitParkCalls(t, recorder, true)
	UpdateHdmiViewerSnapshot("webrtc", 1, 2)
	expectNoMoreParkCalls(t, recorder, true)
}

func TestDisabledHdmiIsNotParked(t *testing.T) {
	recorder := withParkHooks(t)
	isHdmiDisabled = func() bool { return true }
	UpdateHdmiViewerSnapshot("webrtc", 0, 1)
	expectNoMoreParkCalls(t, recorder)
}

// Changing the idle timeout cancels the pending park timer; it must be set
// again.
func TestIdleTimeoutChangeKeepsParking(t *testing.T) {
	recorder := withParkHooks(t)
	hdmiCaptureParkDelay = 200 * time.Millisecond
	UpdateHdmiViewerSnapshot("webrtc", 0, 1)
	SetHdmiIdleTimeout(5)
	waitParkCalls(t, recorder, true)
}

func TestParkedCaptureRetriesReclamationUntilViewerReturns(t *testing.T) {
	recorder := withParkHooks(t)
	recorder.pending = true
	hdmiCaptureReclaimDelay = 20 * time.Millisecond
	UpdateHdmiViewerSnapshot("webrtc", 0, 1)
	deadline := time.Now().Add(5 * time.Second)
	for len(recorder.snapshot()) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	hdmiMutex.Lock()
	if len(recorder.calls) < 3 {
		hdmiMutex.Unlock()
		t.Fatal("parked capture never retried reclamation")
	}
	for _, parked := range recorder.calls {
		if !parked {
			hdmiMutex.Unlock()
			t.Fatal("reclamation re-enabled capture")
		}
	}
	// A failed repeat must retain the parked state and arrange another retry.
	recorder.result = false
	beforeFailure := len(recorder.calls)
	hdmiMutex.Unlock()
	deadline = time.Now().Add(5 * time.Second)
	for len(recorder.snapshot()) < beforeFailure+2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	hdmiMutex.Lock()
	if !hdmiCaptureParked || len(recorder.calls) < beforeFailure+2 {
		hdmiMutex.Unlock()
		t.Fatal("failed repeated park lost state or stopped retrying")
	}
	recorder.result = true
	hdmiMutex.Unlock()
	UpdateHdmiViewerSnapshot("webrtc", 1, 2)
	calls := recorder.snapshot()
	if calls[len(calls)-1] {
		t.Fatal("new viewer did not resume capture")
	}
	time.Sleep(5 * hdmiCaptureReclaimDelay)
	if got := recorder.snapshot(); !reflect.DeepEqual(got, calls) {
		t.Fatalf("reclamation continued with a viewer: %v -> %v", calls, got)
	}
}

func TestParkTimerStopsWhenAllPoolsAreReclaimed(t *testing.T) {
	recorder := withParkHooks(t)
	hdmiCaptureReclaimDelay = 20 * time.Millisecond
	UpdateHdmiViewerSnapshot("webrtc", 0, 1)
	waitParkCalls(t, recorder, true)
	expectNoMoreParkCalls(t, recorder, true)
}

func TestParkTimerStopsAfterDelayedPoolReclaim(t *testing.T) {
	recorder := withParkHooks(t)
	recorder.pending = true
	hdmiCaptureReclaimDelay = 20 * time.Millisecond
	UpdateHdmiViewerSnapshot("webrtc", 0, 1)
	deadline := time.Now().Add(5 * time.Second)
	for len(recorder.snapshot()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	hdmiMutex.Lock()
	if len(recorder.calls) < 2 {
		hdmiMutex.Unlock()
		t.Fatal("pending pool was not retried")
	}
	recorder.pending = false
	hdmiMutex.Unlock()
	deadline = time.Now().Add(5 * time.Second)
	for {
		hdmiMutex.Lock()
		done := hdmiParkTimer == nil
		hdmiMutex.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reclaim timer did not stop")
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := recorder.snapshot()
	time.Sleep(5 * hdmiCaptureReclaimDelay)
	if got := recorder.snapshot(); !reflect.DeepEqual(got, calls) {
		t.Fatalf("reclamation continued after completion: %v -> %v", calls, got)
	}
}
