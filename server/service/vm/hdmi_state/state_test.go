package hdmi_state

import (
	"testing"
	"time"
)

func TestUpdateViewerIgnoresOlderSnapshot(t *testing.T) {
	state := New()
	if !state.UpdateViewer("direct", 1, 2) {
		t.Fatal("new snapshot was rejected")
	}
	if state.UpdateViewer("direct", 0, 1) {
		t.Fatal("older snapshot was accepted")
	}

	count, version, ok := state.Viewer("direct")
	if !ok || count != 1 || version != 2 || state.ViewerCount() != 1 {
		t.Fatalf("viewer=(%d,%d,%t) total=%d", count, version, ok, state.ViewerCount())
	}
}

func TestClaimFreshFrameOnlySucceedsOnce(t *testing.T) {
	state := New()
	state.MarkWarming(time.Now())

	if !state.ClaimFreshFrame() {
		t.Fatal("first reader did not claim the fresh frame")
	}
	if state.ClaimFreshFrame() {
		t.Fatal("second reader claimed the same fresh frame")
	}
}

func (s *State) Viewer(source string) (count int, version uint64, ok bool) {
	value, ok := s.sources[source]
	return value.count, value.version, ok
}
