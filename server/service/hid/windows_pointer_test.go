package hid

import (
	"bytes"
	"testing"
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
