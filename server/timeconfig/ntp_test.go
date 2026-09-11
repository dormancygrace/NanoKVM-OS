package timeconfig

import (
	"encoding/binary"
	"testing"
)

func TestNTPStatus(t *testing.T) {
	reply := make([]byte, 12)
	reply[0] = 4<<3 | 6
	reply[1] = 0x80 | 2
	binary.BigEndian.PutUint16(reply[2:4], 42)
	for _, test := range []struct {
		status uint16
		want   bool
	}{{0x0615, true}, {0xc016, false}, {0xc615, false}, {0x4615, true}, {0x8615, true}, {0x0515, false}} {
		binary.BigEndian.PutUint16(reply[4:6], test.status)
		got, err := parseNTPStatus(reply, 42)
		if err != nil || got != test.want {
			t.Fatalf("status %04x: %v %v", test.status, got, err)
		}
	}
	if _, err := parseNTPStatus(reply, 43); err == nil {
		t.Fatal("wrong sequence accepted")
	}
	if _, err := parseNTPStatus(reply[:8], 42); err == nil {
		t.Fatal("short reply accepted")
	}
	reply[1] |= 0x40
	if _, err := parseNTPStatus(reply, 42); err == nil {
		t.Fatal("error reply accepted")
	}
	reply[1] &= ^byte(0x40)
	reply[10] = 1
	if _, err := parseNTPStatus(reply, 42); err == nil {
		t.Fatal("truncated payload accepted")
	}
}
