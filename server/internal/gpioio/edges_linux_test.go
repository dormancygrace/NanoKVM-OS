//go:build linux

package gpioio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestEdgeRequestIsInputWithBothEdges(t *testing.T) {
	r := edgeRequest(25)
	if r.Num_lines != 1 || r.Offsets[0] != 25 || r.Config.Num_attrs != 0 {
		t.Fatal(r)
	}
	if r.Config.Flags != flagInput|flagEdgeRising|flagEdgeFalling {
		t.Fatalf("flags = %#x", r.Config.Flags)
	}
}

func TestUnsupportedEdgesClassifiesErrnos(t *testing.T) {
	for _, errno := range []error{unix.EINVAL, unix.ENXIO, unix.EOPNOTSUPP, errnoENOTSUPP} {
		err := unsupportedEdges(fmt.Errorf("request gpio-v2:chip:1: %w", errno))
		if !errors.Is(err, ErrEdgesUnsupported) || !errors.Is(err, errno) {
			t.Fatalf("%v not marked unsupported: %v", errno, err)
		}
	}
	for _, errno := range []error{unix.EBUSY, unix.EACCES, os.ErrClosed} {
		if err := unsupportedEdges(errno); errors.Is(err, ErrEdgesUnsupported) {
			t.Fatalf("%v marked unsupported", errno)
		}
	}
	if _, err := OpenEdges("/sys/class/gpio/gpio504/value"); !errors.Is(err, ErrEdgesUnsupported) {
		t.Fatalf("sysfs line: %v", err)
	}
}

// A pipe stands in for a line request fd: both are pollable character streams.
func newPipeEdgeLine(t *testing.T) (*edgeLine, *os.File) {
	t.Helper()
	var fds [2]int
	if err := unix.Pipe2(fds[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	line, err := newEdgeLine(fds[0])
	if err != nil {
		unix.Close(fds[1])
		t.Fatal(err)
	}
	writer := os.NewFile(uintptr(fds[1]), "pipe")
	t.Cleanup(func() { line.Close(); writer.Close() })
	return line, writer
}

func edgeRecord(t *testing.T, age time.Duration) []byte {
	t.Helper()
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &now); err != nil {
		t.Fatal(err)
	}
	record := make([]byte, edgeEventSize)
	binary.NativeEndian.PutUint64(record, uint64(now.Nano()-int64(age)))
	return record
}

func TestEdgeLineDrainsQueuedEventsInOneRead(t *testing.T) {
	line, writer := newPipeEdgeLine(t)
	batch := append(edgeRecord(t, 300*time.Millisecond), edgeRecord(t, 100*time.Millisecond)...)
	batch = append(batch, edgeRecord(t, 200*time.Millisecond)...)
	if _, err := writer.Write(batch); err != nil {
		t.Fatal(err)
	}
	events, err := line.WaitEdges()
	if err != nil || events.Count != 3 {
		t.Fatalf("events = %+v, err = %v", events, err)
	}
	if age := time.Since(events.Last); age < 100*time.Millisecond || age > 150*time.Millisecond {
		t.Fatalf("newest event mapped to %v ago, want 100ms", age)
	}
}

func TestEdgeLineRejectsPartialEvents(t *testing.T) {
	line, writer := newPipeEdgeLine(t)
	if _, err := writer.Write(edgeRecord(t, 0)[:edgeEventSize-1]); err != nil {
		t.Fatal(err)
	}
	if _, err := line.WaitEdges(); err == nil {
		t.Fatal("partial event accepted")
	}
}

func TestEdgeLineCloseUnblocksWait(t *testing.T) {
	line, _ := newPipeEdgeLine(t)
	done := make(chan error, 1)
	go func() {
		_, err := line.WaitEdges()
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	line.Close()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) || errors.Is(err, ErrEdgesUnsupported) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock WaitEdges")
	}
}

func TestEdgeLineRequiresPollableFD(t *testing.T) {
	fd, err := unix.Open(filepath.Join(t.TempDir(), "line"), unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newEdgeLine(fd); !errors.Is(err, ErrEdgesUnsupported) {
		t.Fatalf("regular file accepted: %v", err)
	}
}
