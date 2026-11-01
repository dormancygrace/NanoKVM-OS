//go:build linux

package udpbatch

import (
	"bytes"
	"net/netip"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func sizedGSOMedia(n int, value byte) []byte {
	b := make([]byte, n)
	copy(b, mediaPacket(value, false))
	for i := 16; i < len(b); i++ {
		b[i] = value
	}
	return b
}

func queueGSOTestPackets(t *testing.T, sender *Conn, destinationAddress string, sizes []int) [][]byte {
	t.Helper()
	destination, err := netip.ParseAddrPort(destinationAddress)
	if err != nil {
		t.Fatal(err)
	}
	sender.BeginFrame()
	var wanted [][]byte
	for i, n := range sizes {
		packet := sizedGSOMedia(n, byte(i+1))
		wanted = append(wanted, append([]byte(nil), packet...))
		if _, err = sender.WriteToAddrPort(packet, destination); err != nil {
			t.Fatal(err)
		}
		clear(packet) // caller buffers may be reused immediately
	}
	return wanted
}

func TestGSOLoopbackWireEquality(t *testing.T) {
	for _, network := range []string{"udp4", "udp6"} {
		t.Run(network, func(t *testing.T) {
			t.Setenv("NANOKVM_WEBRTC_UDP_GSO", "1")
			sender, receiver, destination := openPair(t, network)
			sender.flushInterval = time.Hour
			var gsos atomic.Int32
			sender.sendmmsg = func(fd uintptr, p unsafe.Pointer, n uintptr) (uintptr, syscall.Errno) {
				if (*mmsghdr)(p).msg.Controllen != 0 {
					gsos.Add(1)
				}
				return syscallSendmmsg(fd, p, n)
			}
			want := queueGSOTestPackets(t, sender, destination.AddrPort().String(), []int{1200, 1200, 1200, 211})
			if err := sender.EndFrame(); err != nil {
				t.Fatal(err)
			}
			got := readDatagrams(t, receiver, len(want))
			for i := range want {
				if !bytes.Equal(got[i], want[i]) {
					t.Fatalf("datagram%d changed", i)
				}
			}
			if gsos.Load() != 1 || sender.gsoBuffer == nil {
				t.Fatal("GSO was not used successfully")
			}
			if !bytes.Equal(sender.gsoBuffer[:], make([]byte, len(sender.gsoBuffer))) {
				t.Fatal("GSO scratch retained bytes")
			}
		})
	}
}
