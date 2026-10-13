package vm

import (
	"reflect"
	"testing"
	"time"

	hdmistate "NanoKVM-Server/service/vm/hdmi_state"
)

type parkRecorder struct {
	calls  []bool
	result bool
}

func (r *parkRecorder) snapshot() []bool {
	hdmiMutex.Lock()
	defer hdmiMutex.Unlock()
	return append([]bool{}, r.calls...)
}

func withParkHooks(t *testing.T) *parkRecorder {
	t.Helper()
	recorder := &parkRecorder{result: true}
	oldPark, oldDisabled, oldTimeout, oldPersist, oldHDMI, oldDelay := setCaptureParked, isHdmiDisabled, getHdmiIdleTimeout, persistHdmiIdleTimeout, setHDMI, hdmiCaptureParkDelay
	setCaptureParked = func(parked bool) bool {
		recorder.calls = append(recorder.calls, parked)
		return recorder.result
	}
	isHdmiDisabled = func() bool { return false }
	getHdmiIdleTimeout = func() int { return 0 }
	persistHdmiIdleTimeout = func(int) {}
	setHDMI = func(bool) {}
	hdmiCaptureParkDelay = 20 * time.Millisecond
	reset := func() {
		hdmiMutex.Lock()
		stopHdmiParkTimerLocked()
		stopHdmiIdleTimerLocked()
		hdmiIdleGeneration++
		hdmiDemand = hdmistate.New()
		hdmiCaptureParked = false
		hdmiStoppedForIdle = false
		hdmiMutex.Unlock()
	}
	reset()
	t.Cleanup(func() {
		reset()
		setCaptureParked, isHdmiDisabled, getHdmiIdleTimeout, persistHdmiIdleTimeout, setHDMI, hdmiCaptureParkDelay = oldPark, oldDisabled, oldTimeout, oldPersist, oldHDMI, oldDelay
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
