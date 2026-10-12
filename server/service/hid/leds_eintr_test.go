package hid

import (
	"syscall"
	"testing"
	"time"
)

// The Go runtime interrupts threads with SIGURG; poll(2) then fails with
// EINTR. The wait must keep waiting until the notifier fires.
func TestWaitForKeyboardLedReaderChangeSurvivesSignals(t *testing.T) {
	h := &Hid{}
	h.kbMutex.Lock()
	if err := h.ensureKeyboardLedReaderNotifierNoLock(); err != nil {
		h.kbMutex.Unlock()
		t.Fatal(err)
	}
	notifierFD := h.ledReaderNotifyReadFD
	h.kbMutex.Unlock()
	defer h.ledReaderNotifyReader.Close()
	defer h.ledReaderNotifyWriter.Close()

	done := make(chan error, 1)
	go func() { done <- waitForKeyboardLedReaderChange(notifierFD) }()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGURG); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			t.Fatalf("wait returned before a notification: %v", err)
		case <-time.After(time.Millisecond):
		}
	}

	h.kbMutex.Lock()
	h.notifyKeyboardLedReaderNoLock()
	h.kbMutex.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not return after a notification")
	}
}
