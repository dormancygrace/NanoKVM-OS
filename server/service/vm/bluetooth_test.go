package vm

import (
	"encoding/binary"
	"testing"
)

func TestBluetoothCoreVersion(t *testing.T) {
	for hci, want := range map[uint8]string{9: "5.0", 11: "5.2", 13: "5.4", 14: "6.0", 99: "HCI 99"} {
		if got := bluetoothCoreVersion(hci); got != want {
			t.Errorf("%d: %q, want %q", hci, got, want)
		}
	}
}

func TestBluetoothReadInfoReply(t *testing.T) {
	ev := func(code, index, opcode uint16, status byte, params ...byte) []byte {
		b := make([]byte, 9, 9+len(params))
		binary.LittleEndian.PutUint16(b[0:], code)
		binary.LittleEndian.PutUint16(b[2:], index)
		binary.LittleEndian.PutUint16(b[4:], uint16(3+len(params)))
		binary.LittleEndian.PutUint16(b[6:], opcode)
		b[8] = status
		return append(b, params...)
	}
	reply := ev(1, 0, 4, 0, 1, 2, 3, 4, 5, 6, 11, 0x60, 0)
	if v, done, err := parseBluetoothReadInfo(reply, 0); !done || err != nil || v != "5.2" {
		t.Fatalf("reply: %q %v %v", v, done, err)
	}
	// Other indexes, other commands and other events are skipped.
	for _, other := range [][]byte{ev(1, 1, 4, 0, make([]byte, 7)...), ev(1, 0, 5, 0, make([]byte, 7)...), ev(6, 0, 4, 0), {1, 0}} {
		if _, done, _ := parseBluetoothReadInfo(other, 0); done {
			t.Fatalf("took %v", other)
		}
	}
	if _, done, err := parseBluetoothReadInfo(ev(2, 0, 4, 0x11), 0); !done || err == nil {
		t.Fatal("status error not reported")
	}
	if _, done, err := parseBluetoothReadInfo(ev(1, 0, 4, 0, 1, 2), 0); !done || err == nil {
		t.Fatal("short reply accepted")
	}
}
