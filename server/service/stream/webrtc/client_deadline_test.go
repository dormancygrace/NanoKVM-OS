package webrtc

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestDeadlineIncludesAnExistingBlockedWriter(t *testing.T) {
	ws := testConn(t)
	tcp := ws.UnderlyingConn().(*net.TCPConn)
	if err := tcp.SetWriteBuffer(4096); err != nil {
		t.Fatal(err)
	}
	c := &Client{ws: ws}
	first := make(chan error, 1)
	go func() { first <- c.WriteMessage("blocked", strings.Repeat("x", 16<<20)) }()
	deadline := time.Now().Add(time.Second)
	for {
		if !c.mutex.TryLock() {
			break
		}
		c.mutex.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("writer did not start")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	select {
	case err := <-first:
		t.Fatalf("initial write did not block: %v", err)
	default:
	}
	ended := make(chan error, 1)
	start := time.Now()
	go func() { ended <- c.writeMessageWithin("video-error", "encoder-reconfigured", 40*time.Millisecond) }()
	pending := true
	select {
	case <-ended:
		pending = false
	case <-time.After(250 * time.Millisecond):
	}
	elapsed := time.Since(start)
	_ = ws.Close()
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("initial writer leaked")
	}
	if pending {
		select {
		case <-ended:
		case <-time.After(time.Second):
			t.Fatal("ended notifier leaked")
		}
	}
	t.Logf("40ms bounded notification completed before cleanup: %t, elapsed %v", !pending, elapsed)
	if pending {
		t.Fatal("writeMessageWithin cannot reach its deadline while an existing writer owns the mutex")
	}
}
