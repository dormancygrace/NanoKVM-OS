//go:build linux

package udpbatch

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestLoopbackExactOrderAndCallerReuse(t *testing.T) {
	for _, network := range []string{"udp4", "udp6"} {
		network := network
		t.Run(network, func(t *testing.T) {
			sender, receiver, destination := openPair(t, network)
			first := mediaPacket(1, false)
			firstWant := append([]byte(nil), first...)
			second := mediaPacket(2, true)
			sender.BeginFrame()
			if n, err := sender.WriteToAddrPort(first, destination.AddrPort()); err != nil || n != len(first) {
				t.Fatalf("first WriteToAddrPort: n=%d err=%v", n, err)
			}
			for i := range first {
				first[i] = 0
			}
			if n, err := sender.WriteToUDP(second, destination); err != nil || n != len(second) {
				t.Fatalf("second WriteToUDP: n=%d err=%v", n, err)
			}
			if err := sender.EndFrame(); err != nil {
				t.Fatalf("EndFrame: %v", err)
			}
			got := readDatagrams(t, receiver, 2)
			if !bytes.Equal(got[0], firstWant) || !bytes.Equal(got[1], second) {
				t.Fatalf("datagrams changed or reordered: got=%x/%x want=%x/%x", got[0], got[1], firstWant, second)
			}
		})
	}
}

func openPair(t *testing.T, network string) (*Conn, *net.UDPConn, *net.UDPAddr) {
	t.Helper()
	receiver, err := net.ListenUDP(network, loopbackAddr(network))
	if err != nil {
		if network == "udp6" {
			t.Skipf("IPv6 unavailable: %v", err)
		}
		t.Fatal(err)
	}
	raw, err := net.ListenUDP(network, loopbackAddr(network))
	if err != nil {
		receiver.Close()
		t.Fatal(err)
	}
	sender, ok := Wrap(raw, false, nil)
	if !ok {
		raw.Close()
		receiver.Close()
		t.Fatalf("Wrap did not install for %s", network)
	}
	t.Cleanup(func() {
		_ = sender.Close()
		_ = receiver.Close()
	})
	destination := receiver.LocalAddr().(*net.UDPAddr)
	return sender, receiver, destination
}

func loopbackAddr(network string) *net.UDPAddr {
	if network == "udp6" {
		return &net.UDPAddr{IP: net.IPv6loopback}
	}
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

func mediaPacket(value byte, marker bool) []byte {
	b := make([]byte, 16)
	b[0] = 0x80
	b[1] = 102
	if marker {
		b[1] |= 0x80
	}
	b[12] = value
	return b
}

func controlPacket(value byte) []byte {
	b := make([]byte, 16)
	b[0] = 0x80
	b[1] = 200
	b[12] = value
	return b
}

func readDatagrams(t *testing.T, receiver *net.UDPConn, count int) [][]byte {
	t.Helper()
	_ = receiver.SetReadDeadline(time.Now().Add(time.Second))
	got := make([][]byte, 0, count)
	for len(got) < count {
		buf := make([]byte, 4096)
		n, _, err := receiver.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("read datagram %d/%d: %v", len(got)+1, count, err)
		}
		got = append(got, append([]byte(nil), buf[:n]...))
	}
	return got
}

func TestNonMediaAddressChangeAndOversizeFlushDirect(t *testing.T) {
	sender, receiver, destination := openPair(t, "udp4")
	control := controlPacket(30)
	media := mediaPacket(31, false)
	mediaWant := append([]byte(nil), media...)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(media, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.WriteToUDP(control, destination); err != nil {
		t.Fatal(err)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatal(err)
	}
	got := readDatagrams(t, receiver, 2)
	if !bytes.Equal(got[0], mediaWant) || !bytes.Equal(got[1], control) {
		t.Fatalf("non-media order: got=%x/%x want=%x/%x", got[0], got[1], mediaWant, control)
	}

	receiver2, err := net.ListenUDP("udp4", loopbackAddr("udp4"))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver2.Close()
	destination2 := receiver2.LocalAddr().(*net.UDPAddr)
	first := mediaPacket(32, false)
	second := mediaPacket(33, false)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(first, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.WriteToUDP(second, destination2); err != nil {
		t.Fatal(err)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatal(err)
	}
	if got := readDatagrams(t, receiver, 1); !bytes.Equal(got[0], first) {
		t.Fatalf("address-change first: got=%x want=%x", got[0], first)
	}
	if got := readDatagrams(t, receiver2, 1); !bytes.Equal(got[0], second) {
		t.Fatalf("address-change second: got=%x want=%x", got[0], second)
	}

	oversize := append(mediaPacket(34, false), make([]byte, maxPacketBytes)...)
	queued := mediaPacket(35, false)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(queued, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.WriteToUDP(oversize, destination); err != nil {
		t.Fatal(err)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatal(err)
	}
	got = readDatagrams(t, receiver, 2)
	if !bytes.Equal(got[0], queued) || !bytes.Equal(got[1], oversize) {
		t.Fatalf("oversize order: got=%x/%x want=%x/%x", got[0], got[1], queued, oversize)
	}
}

func TestBatchableRTPPayloadTypes(t *testing.T) {
	packet := func(first, second byte) []byte {
		b := make([]byte, 16)
		b[0], b[1] = first, second
		return b
	}
	for _, tc := range []struct {
		name string
		b    []byte
		want bool
	}{
		{"H.264 102", packet(0x80, 102), true},
		{"H.265 126", packet(0x80, 126), true},
		{"H.265 as negotiated by Chrome (49)", packet(0x80, 49), true},
		{"H.265 49 with marker", packet(0x80, 0x80|49), true},
		{"Opus 111", packet(0x80, 111), true},
		{"RTCP SR", packet(0x80, 200), false},
		{"RTCP RR with count", packet(0x81, 201), false},
		{"RTCP feedback", packet(0x81, 205), false},
		{"RTCP XR", packet(0x80, 207), false},
		{"STUN", packet(0x00, 0x01), false},
		{"DTLS handshake", packet(22, 0xfe), false},
		{"too short", []byte{0x80, 102}, false},
	} {
		if got := isBatchableRTP(tc.b); got != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}
