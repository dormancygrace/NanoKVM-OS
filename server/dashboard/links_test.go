package dashboard

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"testing"
)

func attr(kind uint16, payload []byte) []byte {
	n := 4 + len(payload)
	out := make([]byte, (n+3)&^3)
	binary.NativeEndian.PutUint16(out, uint16(n))
	binary.NativeEndian.PutUint16(out[2:], kind)
	copy(out[4:], payload)
	return out
}
func TestWireGuardLinkKind(t *testing.T) {
	inner := attr(unix.IFLA_INFO_KIND, []byte("wireguard\x00"))
	outer := append(attr(unix.IFLA_MTU, []byte{0, 0, 0, 0}), attr(unix.IFLA_LINKINFO|0x8000, inner)...)
	info := linkAttribute(outer, unix.IFLA_LINKINFO)
	if string(linkAttribute(info, unix.IFLA_INFO_KIND)) != "wireguard\x00" {
		t.Fatal("nested WireGuard kind not found")
	}
	for _, bad := range [][]byte{{1, 0, 1, 0}, {200, 0, 1, 0}, {0, 0}, {}} {
		if linkAttribute(bad, 1) != nil {
			t.Fatalf("accepted malformed attribute %v", bad)
		}
	}
}
