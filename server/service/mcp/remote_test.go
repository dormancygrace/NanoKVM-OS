package mcpservice

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingHID struct {
	keyboard [][]byte
	relative [][]byte
	absolute [][]byte
	failAt   int
	writes   int
	onWrite  func(int)
}

func (h *recordingHID) record(target *[][]byte, report []byte) error {
	h.writes++
	if h.onWrite != nil {
		h.onWrite(h.writes)
	}
	*target = append(*target, append([]byte(nil), report...))
	if h.failAt > 0 && h.writes == h.failAt {
		return errors.New("write failed")
	}
	return nil
}

func (h *recordingHID) WriteKeyboardReport(report []byte) error {
	return h.record(&h.keyboard, report)
}

func (h *recordingHID) WriteRelativeMouseReport(report []byte) error {
	return h.record(&h.relative, report)
}

func (h *recordingHID) WriteAbsoluteMouseReport(report []byte) error {
	return h.record(&h.absolute, report)
}

func TestRemotePressKeysAlwaysReleases(t *testing.T) {
	hid := &recordingHID{}
	remote := newRemoteWithHID(hid)
	if err := remote.PressKeys(context.Background(), []string{"ShiftLeft", "KeyA"}, 0); err != nil {
		t.Fatal(err)
	}
	if len(hid.keyboard) != 2 || string(hid.keyboard[1]) != string(keyUpReport) {
		t.Fatalf("keyboard reports: %v", hid.keyboard)
	}

	failing := &recordingHID{failAt: 1}
	if err := newRemoteWithHID(failing).PressKeys(context.Background(), []string{"KeyA"}, 0); err == nil {
		t.Fatal("expected write error")
	}
	if len(failing.keyboard) != 2 || string(failing.keyboard[1]) != string(keyUpReport) {
		t.Fatalf("release not attempted after error: %v", failing.keyboard)
	}
}

func newRemoteWithHID(writer HIDWriter) *Remote {
	return &Remote{hid: writer}
}

func TestDragReleasesWhenCancelledOrFailing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	hid := &recordingHID{}
	hid.onWrite = func(n int) {
		if n == 3 {
			cancel()
		}
	}
	if err := newRemoteWithHID(hid).Drag(ctx, "left", [2]float64{0, 0}, [2]float64{1, 1}, 10, 100*time.Millisecond); err == nil {
		t.Fatal("cancelled drag returned no error")
	}
	if last := hid.absolute[len(hid.absolute)-1]; last[0] != 0 {
		t.Fatalf("cancelled drag left the button held: %v", hid.absolute)
	}

	failing := &recordingHID{failAt: 4}
	if err := newRemoteWithHID(failing).Drag(context.Background(), "right", [2]float64{0, 0}, [2]float64{1, 1}, 10, 0); err == nil {
		t.Fatal("failing drag returned no error")
	}
	if last := failing.absolute[len(failing.absolute)-1]; last[0] != 0 {
		t.Fatalf("failing drag left the button held: %v", failing.absolute)
	}
}
