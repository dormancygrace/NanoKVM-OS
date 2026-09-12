package audio

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPacketFraming(t *testing.T) {
	r := bytes.NewReader([]byte{0, 3, 1, 2, 3, 0, 1, 4})
	for _, want := range [][]byte{{1, 2, 3}, {4}} {
		got, err := readPacket(r)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%v %v", got, err)
		}
	}
	if _, err := readPacket(r); err != io.EOF {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{{0}, {0, 0}, {5, 0}, {0, 3, 1}} {
		if _, err := readPacket(bytes.NewReader(bad)); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}
func TestCaptureCardUsesIdentityNotIndex(t *testing.T) {
	root := t.TempDir()
	for name, id := range map[string]string{"card0": "Other", "card3": "UAC1Gadget\n"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "id"), []byte(id), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := captureCard(root)
	if err != nil || got != "3" {
		t.Fatalf("%q %v", got, err)
	}
	if err := os.Remove(filepath.Join(root, "card3", "id")); err != nil {
		t.Fatal(err)
	}
	if _, err := captureCard(root); err == nil {
		t.Fatal("selected unrelated ALSA card")
	}
}
func TestSlowListenerKeepsFreshTailAndDoesNotBlockOthers(t *testing.T) {
	slow := &listener{frames: make(chan packet, 4), done: make(chan struct{})}
	fast := &listener{frames: make(chan packet, 4), done: make(chan struct{})}
	h := hub{clients: map[*listener]struct{}{slow: {}, fast: {}}}
	for i := uint64(0); i < 100; i++ {
		h.publish(packet{index: i})
		if got := (<-fast.frames).index; got != i {
			t.Fatal(got)
		}
	}
	if len(slow.frames) != 4 {
		t.Fatal(len(slow.frames))
	}
	for i := uint64(96); i < 100; i++ {
		if got := (<-slow.frames).index; got != i {
			t.Fatal(got)
		}
	}
	slow.close()
	slow.close() // USB stop and process exit can both close a listener.
}
