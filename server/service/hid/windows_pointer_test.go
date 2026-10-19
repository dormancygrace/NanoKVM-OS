package hid

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

func TestWindowsPointerReports(t *testing.T) {
	for _, tc := range []struct{ buttons, flags, pressure byte }{{0, 4, 0}, {1, 5, 4}, {2, 7, 4}, {3, 7, 4}, {0x1c, 4, 0}} {
		r := windowsPointerReports([]byte{tc.buttons, 0x34, 0x12, 0x78, 0x56, 0xff, 2})
		if !bytes.Equal(r[0], []byte{1, tc.flags, 0x34, 0x12, 0x78, 0x56, 0, tc.pressure}) {
			t.Fatalf("pen %x", r[0])
		}
		if !bytes.Equal(r[1], []byte{2, tc.buttons & 0x1c, 0, 0, 0xff, 2}) {
			t.Fatalf("mouse %x", r[1])
		}
	}
}

// App-only restarts leave the old gadget descriptor live until USB rebind.
func TestAbsoluteReportWithLiveRetiredDescriptor(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		h := &Hid{g2: writer, windowsPointer: legacy}
		input := []byte{1, 0x34, 0x12, 0x78, 0x56, 0xff, 2}
		want := input
		if legacy {
			want = []byte{1, 5, 0x34, 0x12, 0x78, 0x56, 0, 4, 2, 0, 0, 0, 0xff, 2}
		}
		if err := h.WriteAbsoluteMouseReport(input); err != nil {
			t.Fatal(err)
		}
		reader.SetReadDeadline(time.Now().Add(time.Second))
		got := make([]byte, len(want))
		_, err = io.ReadFull(reader, got)
		reader.Close()
		writer.Close()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("legacy=%v: %x, %v; want %x", legacy, got, err, want)
		}
	}
}
