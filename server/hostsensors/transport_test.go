package hostsensors

import (
	"net"
	"strings"
	"testing"
)

// Lines arrive in arbitrary pieces; a line longer than a report is dropped
// to its end and the next one is read.
func TestSplitLines(t *testing.T) {
	var got []string
	accept := func(line []byte) { got = append(got, string(line)) }
	var line []byte
	overflow := false
	for _, chunk := range []string{"{\"a\"", ":1}\r\n\n{\"b\":2}\n{\"c\"", strings.Repeat("x", MaxReportBytes), "tail\n{\"d\":4}\n"} {
		line, overflow = SplitLines(line, overflow, []byte(chunk), accept)
	}
	want := []string{`{"a":1}`, `{"b":2}`, "", `{"d":4}`}
	if strings.Join(got, "|") != strings.Join(want, "|") || overflow || len(line) != 0 {
		t.Fatalf("got %q, overflow %v, pending %d", got, overflow, len(line))
	}
}

// Readings are taken from the USB network's peers only.
func TestFromNetwork(t *testing.T) {
	_, usb, _ := net.ParseCIDR("10.20.30.1/24")
	usb.IP = net.ParseIP("10.20.30.1")
	nets := []*net.IPNet{usb}
	for addr, want := range map[string]bool{
		"10.20.30.100:50000": true,
		"10.20.30.1:50000":   false, // the NanoKVM itself
		"192.168.1.5:50000":  false, // the LAN
		"[::1]:50000":        false,
		"10.20.30.100":       false,
	} {
		if got := FromNetwork(addr, nets); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
	if FromNetwork("10.20.30.100:1", nil) {
		t.Error("accepted without a USB network")
	}
}
