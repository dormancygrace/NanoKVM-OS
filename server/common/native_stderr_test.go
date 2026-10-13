package common

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
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

func TestNativeStderrReaderSurvivesLongLines(t *testing.T) {
	long := strings.Repeat("x", 70<<10)
	lines := make(chan string, 8)
	readNativeStderr(strings.NewReader(long+"\nAFTER\nlast without newline"), lines)
	var got []string
	for line := range lines {
		got = append(got, line)
	}
	if len(got) != 3 || got[1] != "AFTER" || got[2] != "last without newline" {
		t.Fatalf("lines %d: %.40q", len(got), got)
	}
	if !strings.HasSuffix(got[0], " [cut]") || len(got[0]) != nativeLineBytes+len(" [cut]") {
		t.Fatalf("long line not cut: %d bytes", len(got[0]))
	}
}

type failingReader struct{ data string }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.data == "" {
		return 0, errors.New("read failed")
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestNativeStderrReaderStopsOnReadError(t *testing.T) {
	lines := make(chan string, 8)
	readNativeStderr(&failingReader{data: "a\npartial"}, lines)
	var got []string
	for line := range lines {
		got = append(got, line)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "partial" {
		t.Fatalf("got %q", got)
	}
}

// A process that execs itself must not leave the capture pipe on descriptor
// 2: its read end closes on exec, and the new image dies of SIGPIPE at its
// first line on stderr. The child captures stderr, optionally restores it,
// and execs a shell that writes to stderr.
func TestRestoreNativeStderrBeforeExec(t *testing.T) {
	if mode := os.Getenv("NANOKVM_STDERR_EXEC_CHILD"); mode != "" {
		if err := CaptureNativeStderr(io.Discard); err != nil {
			os.Exit(3)
		}
		if mode == "restore" {
			RestoreNativeStderr()
		}
		_ = syscall.Exec("/bin/sh", []string{"sh", "-c", "echo started >&2"}, os.Environ())
		os.Exit(4)
	}
	run := func(mode string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRestoreNativeStderrBeforeExec$")
		cmd.Env = append(os.Environ(), "NANOKVM_STDERR_EXEC_CHILD="+mode)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stderr.String(), err
	}
	if out, err := run("restore"); err != nil || !strings.Contains(out, "started") {
		t.Fatalf("restored: %v, stderr %q", err, out)
	}
	_, err := run("keep")
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("without restoring: %v, want SIGPIPE", err)
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != syscall.SIGPIPE {
		t.Fatalf("without restoring: %v, want SIGPIPE", err)
	}
}
