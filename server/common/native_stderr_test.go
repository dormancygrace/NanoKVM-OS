package common

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNativeStderrRateLimit(t *testing.T) {
	lines := make(chan string, 100)
	for i := 0; i < 30; i++ {
		lines <- "[kvm_mmf] frame still pending"
	}
	lines <- "next second"
	close(lines)

	start := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	calls := 0
	now := func() time.Time {
		calls++
		if calls > 30 {
			return start.Add(time.Second)
		}
		return start
	}
	var out bytes.Buffer
	writeNativeStderr(lines, &out, now)

	got := out.String()
	if n := strings.Count(got, "frame still pending"); n != nativeLinesPerSecond {
		t.Fatalf("%d lines in the first second, want %d:\n%s", n, nativeLinesPerSecond, got)
	}
	if !strings.Contains(got, "[native] 10 more lines dropped") || !strings.Contains(got, "[native] next second") {
		t.Fatalf("missing drop summary or later line:\n%s", got)
	}
}

func TestNativeStderrReaderNeverBlocks(t *testing.T) {
	lines := make(chan string, 2)
	done := make(chan struct{})
	go func() {
		readNativeStderr(strings.NewReader("a\nb\nc\nd\n"), lines)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader blocked on a full queue")
	}
	if got := []string{<-lines, <-lines}; got[0] != "a" || got[1] != "b" {
		t.Fatalf("queued %q", got)
	}
}
