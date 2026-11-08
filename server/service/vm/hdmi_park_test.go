package vm

import (
	"sync"
	"testing"
	"time"

	hdmistate "NanoKVM-Server/service/vm/hdmi_state"
)

// parkRecorder replaces the capture seams and records every park (true) and
// resume (false) request.
type parkRecorder struct {
	mu    sync.Mutex
	calls []bool
}

func (r *parkRecorder) record(parked bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, parked)
	return true
}

func (r *parkRecorder) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.calls...)
}

func stubParkSeams(t *testing.T) *parkRecorder {
	t.Helper()
	r := &parkRecorder{}
	prevSetHDMI, prevDisabled, prevIdle := setHDMI, isHdmiDisabled, getHdmiIdleTimeout
	prevPark, prevReclaim := setCaptureParked, captureReclaimPending
	prevDelay, prevFirst := hdmiCaptureParkDelay, hdmiCaptureFirstParkDelay
	setHDMI = func(bool) {}
	isHdmiDisabled = func() bool { return false }
	getHdmiIdleTimeout = func() int { return 0 }
	setCaptureParked = r.record
	captureReclaimPending = func() bool { return false }

	hdmiMutex.Lock()
	stopHdmiParkTimerLocked()
	stopHdmiIdleTimerLocked()
	hdmiDemand = hdmistate.New()
	hdmiCaptureHadDemand, hdmiCaptureParked, hdmiStoppedForIdle = false, false, false
	hdmiMutex.Unlock()

	t.Cleanup(func() {
		hdmiMutex.Lock()
		stopHdmiParkTimerLocked()
		stopHdmiIdleTimerLocked()
		hdmiDemand = hdmistate.New()
		hdmiCaptureHadDemand, hdmiCaptureParked, hdmiStoppedForIdle = false, false, false
		hdmiMutex.Unlock()
		setHDMI, isHdmiDisabled, getHdmiIdleTimeout = prevSetHDMI, prevDisabled, prevIdle
		setCaptureParked, captureReclaimPending = prevPark, prevReclaim
		hdmiCaptureParkDelay, hdmiCaptureFirstParkDelay = prevDelay, prevFirst
	})
	return r
}

// Before anyone has asked for the picture, the capture parks without the
// reload delay; after a viewer has come and gone, the delay applies again.
func TestCaptureParksAtOnceUntilTheFirstDemand(t *testing.T) {
	r := stubParkSeams(t)
	hdmiCaptureParkDelay = time.Hour
	hdmiCaptureFirstParkDelay = 0

	EnableHdmiCapture()
	deadline := time.Now().Add(2 * time.Second)
	for len(r.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := r.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("calls after start = %v; want one park", got)
	}

	release := AcquireHdmiCaptureLease()
	if got := r.snapshot(); len(got) != 2 || got[1] {
		t.Fatalf("calls after a lease = %v; want park, resume", got)
	}
	release()
	time.Sleep(100 * time.Millisecond)
	if got := r.snapshot(); len(got) != 2 {
		t.Fatalf("calls after the lease ended = %v; the reload delay must apply", got)
	}
	hdmiMutex.Lock()
	scheduled := hdmiParkTimer != nil
	hdmiMutex.Unlock()
	if !scheduled {
		t.Fatal("no park scheduled after the last reader left")
	}
}
