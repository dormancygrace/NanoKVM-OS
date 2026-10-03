package hid

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestHIDFileWriterBoundsUnconsumedReports(t *testing.T) {
	reader, file, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer file.Close()
	writer, err := newHIDFileWriter(file)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 4096)
	for {
		_, err = writer.Write(data)
		if errors.Is(err, syscall.EAGAIN) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	err = writeWithTimeout(writer, []byte{2, 0, 0, 0, 0, 0}, 20*time.Millisecond)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("full HID queue: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("timeout did not bound syscall retries: %s", elapsed)
	}
}
