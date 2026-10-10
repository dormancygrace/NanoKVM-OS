//go:build linux

package udpbatch

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
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

func TestTimerAndControlFlushOrdering(t *testing.T) {
	sender, receiver, destination := openPair(t, "udp4")
	media := mediaPacket(3, false)
	wantMedia := append([]byte(nil), media...)
	control := controlPacket(4)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(media, destination); err != nil {
		t.Fatalf("media WriteToUDP: %v", err)
	}
	for i := range media {
		media[i] = 0
	}
	if _, err := sender.WriteToUDP(control, destination); err != nil {
		t.Fatalf("control WriteToUDP: %v", err)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatalf("EndFrame: %v", err)
	}
	got := readDatagrams(t, receiver, 2)
	if !bytes.Equal(got[0], wantMedia) || !bytes.Equal(got[1], control) {
		t.Fatalf("control ordering: got=%x/%x want=%x/%x", got[0], got[1], wantMedia, control)
	}

	timerMedia := mediaPacket(5, false)
	wantTimer := append([]byte(nil), timerMedia...)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(timerMedia, destination); err != nil {
		t.Fatalf("timer WriteToUDP: %v", err)
	}
	for i := range timerMedia {
		timerMedia[i] = 0
	}
	time.Sleep(3 * time.Millisecond)
	if err := sender.EndFrame(); err != nil {
		t.Fatalf("timer EndFrame: %v", err)
	}
	got = readDatagrams(t, receiver, 1)
	if !bytes.Equal(got[0], wantTimer) {
		t.Fatalf("timer datagram changed: got=%x want=%x", got[0], wantTimer)
	}
}

func TestUnsupportedSendmmsgFallsBackWithoutResend(t *testing.T) {
	sender, receiver, destination := openPair(t, "udp4")
	var calls atomic.Int32
	sender.sendmmsg = func(fd uintptr, messages unsafe.Pointer, count uintptr) (uintptr, syscall.Errno) {
		calls.Add(1)
		return ^uintptr(0), syscall.ENOSYS
	}
	first := mediaPacket(6, false)
	second := mediaPacket(7, false)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(first, destination); err != nil {
		t.Fatalf("first WriteToUDP: %v", err)
	}
	if _, err := sender.WriteToUDP(second, destination); err != nil {
		t.Fatalf("second WriteToUDP: %v", err)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatalf("EndFrame fallback: %v", err)
	}
	got := readDatagrams(t, receiver, 2)
	if !bytes.Equal(got[0], first) || !bytes.Equal(got[1], second) {
		t.Fatalf("fallback datagrams: got=%x/%x want=%x/%x", got[0], got[1], first, second)
	}
	if calls.Load() != 1 {
		t.Fatalf("sendmmsg calls=%d, want one unsupported attempt", calls.Load())
	}
}

func TestPartialSendmmsgSendsUnsentSuffixOnce(t *testing.T) {
	sender, receiver, destination := openPair(t, "udp4")
	var calls atomic.Int32
	sender.sendmmsg = func(fd uintptr, messages unsafe.Pointer, count uintptr) (uintptr, syscall.Errno) {
		if calls.Add(1) == 1 {
			n, _ := syscallSendmmsg(fd, messages, 1)
			return n, syscall.EIO
		}
		return 0, syscall.EIO
	}
	first := mediaPacket(8, false)
	second := mediaPacket(9, false)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(first, destination); err != nil {
		t.Fatalf("first WriteToUDP: %v", err)
	}
	if _, err := sender.WriteToUDP(second, destination); err != nil {
		t.Fatalf("second WriteToUDP: %v", err)
	}
	err := sender.EndFrame()
	if err == nil {
		t.Fatal("partial EndFrame succeeded despite injected send error")
	}
	got := readDatagrams(t, receiver, 2)
	if !bytes.Equal(got[0], first) || !bytes.Equal(got[1], second) {
		t.Fatalf("partial datagrams: got=%x/%x want=%x/%x", got[0], got[1], first, second)
	}
	if calls.Load() != 1 {
		t.Fatalf("sendmmsg calls=%d, want one partial attempt", calls.Load())
	}
}

func TestDeadlineAndCloseInterruptFlush(t *testing.T) {
	sender, _, destination := openPair(t, "udp4")
	sender.sendmmsg = func(fd uintptr, messages unsafe.Pointer, count uintptr) (uintptr, syscall.Errno) {
		return ^uintptr(0), syscall.EAGAIN
	}
	if err := sender.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(mediaPacket(10, false), destination); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if err := sender.EndFrame(); err == nil {
		t.Fatal("EndFrame succeeded despite expired send deadline")
	} else {
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("deadline error=%v, want timeout", err)
		}
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("Close after deadline: %v", err)
	}
	if _, err := sender.WriteToUDP(mediaPacket(11, false), destination); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("write after Close: %v", err)
	}
	if err := sender.EndFrame(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("EndFrame after Close: %v", err)
	}

	sender, _, destination = openPair(t, "udp4")
	sender.sendmmsg = func(fd uintptr, messages unsafe.Pointer, count uintptr) (uintptr, syscall.Errno) {
		return 0, syscall.EAGAIN
	}
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(mediaPacket(12, false), destination); err != nil {
		t.Fatalf("blocking queue: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- sender.EndFrame() }()
	time.Sleep(5 * time.Millisecond)
	closeDone := make(chan error, 1)
	go func() { closeDone <- sender.Close() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("flush completed without close error")
		}
	case <-time.After(time.Second):
		t.Fatal("EndFrame did not unblock after Close")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not complete")
	}
}

// BenchmarkRTPDatagramWrite compares the opt-in frame wrapper with direct
// net.UDPConn writes while a local receiver drains the socket.
func BenchmarkRTPDatagramWrite(b *testing.B) {
	for _, batch := range []bool{false, true} {
		name := "direct"
		if batch {
			name = "sendmmsg_frame16"
		}
		b.Run(name, func(b *testing.B) {
			receiver, err := net.ListenUDP("udp4", loopbackAddr("udp4"))
			if err != nil {
				b.Fatal(err)
			}
			raw, err := net.ListenUDP("udp4", loopbackAddr("udp4"))
			if err != nil {
				receiver.Close()
				b.Fatal(err)
			}
			destination := receiver.LocalAddr().(*net.UDPAddr)
			destinationAddrPort := destination.AddrPort()
			var sender *Conn
			if batch {
				var installed bool
				sender, installed = Wrap(raw, false, nil)
				if !installed {
					raw.Close()
					receiver.Close()
					b.Fatal("Wrap did not install")
				}
			}
			var drain sync.WaitGroup
			drain.Add(1)
			go func() {
				defer drain.Done()
				buf := make([]byte, maxPacketBytes)
				for {
					if _, _, readErr := receiver.ReadFromUDP(buf); readErr != nil {
						return
					}
				}
			}()
			defer func() {
				if sender != nil {
					_ = sender.Close()
				} else {
					_ = raw.Close()
				}
				_ = receiver.Close()
				drain.Wait()
			}()

			payload := mediaPacket(0, false)
			payload = append(payload, make([]byte, 1200-len(payload))...)
			b.SetBytes(int64(len(payload)))
			if batch {
				sender.BeginFrame()
				for i := 0; i < maxBatchPackets; i++ {
					if _, err := sender.WriteToAddrPort(payload, destinationAddrPort); err != nil {
						b.Fatal(err)
					}
				}
				if err := sender.EndFrame(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				payload[12] = byte(i)
				if batch && i%maxBatchPackets == 0 {
					sender.BeginFrame()
				}
				if batch {
					if _, err := sender.WriteToAddrPort(payload, destinationAddrPort); err != nil {
						b.Fatal(err)
					}
					if i%maxBatchPackets == maxBatchPackets-1 || i == b.N-1 {
						if err := sender.EndFrame(); err != nil {
							b.Fatal(err)
						}
					}
				} else if _, err := raw.WriteToUDPAddrPort(payload, destinationAddrPort); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
		})
	}
}

func TestSyscallSendmmsgNormalizesRawError(t *testing.T) {
	n, errno := syscallSendmmsg(^uintptr(0), nil, 1)
	if errno == 0 {
		t.Fatal("invalid fd sendmmsg unexpectedly succeeded")
	}
	if n != 0 {
		t.Fatalf("errored sendmmsg returned count %#x, want zero", n)
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

func TestTimerStateReusedAcrossFrames(t *testing.T) {
	sender, receiver, destination := openPair(t, "udp4")
	defer receiver.Close()
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(mediaPacket(20, false), destination); err != nil {
		t.Fatal(err)
	}
	state := sender.timerState
	if state == nil || !sender.timerArmed {
		t.Fatalf("timer was not armed: state=%p armed=%t", state, sender.timerArmed)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatal(err)
	}
	if sender.timerState != state || sender.timerArmed {
		t.Fatalf("timer was not retained stopped: got=%p/%t want=%p/false", sender.timerState, sender.timerArmed, state)
	}
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(mediaPacket(21, false), destination); err != nil {
		t.Fatal(err)
	}
	if sender.timerState != state || !sender.timerArmed {
		t.Fatalf("timer was not reset: got=%p/%t want=%p/true", sender.timerState, sender.timerArmed, state)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatal(err)
	}
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

func TestBatchAddrRejectsZone(t *testing.T) {
	sender, _, _ := openPair(t, "udp4")
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1, Zone: "lo"}
	if _, ok := sender.batchAddr(addr); ok {
		t.Fatal("batchAddr accepted a zoned address")
	}
}

func TestConfiguredFlushIntervalPreservesFrameAndCloseSemantics(t *testing.T) {
	t.Setenv(batchFlushEnv, "4000")
	sender, receiver, destination := openPair(t, "udp4")
	if sender.flushInterval != 4*time.Millisecond {
		t.Fatalf("flush interval=%s, want 4ms", sender.flushInterval)
	}

	immediate := mediaPacket(40, false)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(immediate, destination); err != nil {
		t.Fatal(err)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatalf("EndFrame with 4ms interval: %v", err)
	}
	if got := readDatagrams(t, receiver, 1); !bytes.Equal(got[0], immediate) {
		t.Fatalf("EndFrame datagram: got=%x want=%x", got[0], immediate)
	}

	timerPacket := mediaPacket(41, false)
	sender.BeginFrame()
	if _, err := sender.WriteToUDP(timerPacket, destination); err != nil {
		t.Fatal(err)
	}
	time.Sleep(8 * time.Millisecond)
	if got := readDatagrams(t, receiver, 1); !bytes.Equal(got[0], timerPacket) {
		t.Fatalf("4ms timer datagram: got=%x want=%x", got[0], timerPacket)
	}
	if err := sender.EndFrame(); err != nil {
		t.Fatalf("EndFrame after timer: %v", err)
	}

	sender.BeginFrame()
	if _, err := sender.WriteToUDP(mediaPacket(42, false), destination); err != nil {
		t.Fatal(err)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sender.EndFrame(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("EndFrame after close: %v", err)
	}

	t.Setenv(batchFlushEnv, "2500")
	raw, err := net.ListenUDP("udp4", loopbackAddr("udp4"))
	if err != nil {
		t.Fatal(err)
	}
	fallback, ok := Wrap(raw, false, nil)
	if !ok {
		raw.Close()
		t.Fatal("default fallback Wrap failed")
	}
	defer fallback.Close()
	if fallback.flushInterval != defaultFlushInterval {
		t.Fatalf("unknown value interval=%s, want %s", fallback.flushInterval, defaultFlushInterval)
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
