//go:build linux

package udpbatch

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
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

func TestGSOUnsupportedFallbackWithoutDuplicates(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EINVAL, syscall.EOPNOTSUPP, syscall.ENOPROTOOPT, syscall.ENOSYS} {
		t.Run(errno.Error(), func(t *testing.T) {
			t.Setenv("NANOKVM_WEBRTC_UDP_GSO", "1")
			sender, receiver, destination := openPair(t, "udp4")
			sender.flushInterval = time.Hour
			var attempts atomic.Int32
			sender.sendmmsg = func(fd uintptr, p unsafe.Pointer, n uintptr) (uintptr, syscall.Errno) {
				if (*mmsghdr)(p).msg.Controllen != 0 {
					attempts.Add(1)
					return ^uintptr(0), errno
				}
				return syscallSendmmsg(fd, p, n)
			}
			for repeat := 0; repeat < 2; repeat++ {
				want := queueGSOTestPackets(t, sender, destination.AddrPort().String(), []int{1200, 1200, 211})
				if err := sender.EndFrame(); err != nil {
					t.Fatal(err)
				}
				got := readDatagrams(t, receiver, len(want))
				for i := range want {
					if !bytes.Equal(got[i], want[i]) {
						t.Fatalf("datagram%d changed", i)
					}
				}
			}
			if attempts.Load() != 1 || sender.gsoBuffer != nil {
				t.Fatal("unsupported GSO was not disabled")
			}
			receiver.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
			if n, _, err := receiver.ReadFromUDP(make([]byte, 4096)); err == nil {
				t.Fatalf("duplicate datagram %d bytes", n)
			}
		})
	}
}

func TestGSOUnknownErrorPropagatesWithoutFallback(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EMSGSIZE, syscall.EIO} {
		t.Run(errno.Error(), func(t *testing.T) {
			t.Setenv("NANOKVM_WEBRTC_UDP_GSO", "1")
			sender, receiver, destination := openPair(t, "udp4")
			sender.flushInterval = time.Hour
			var gsoAttempts atomic.Int32
			var ordinaryAttempts atomic.Int32
			sender.sendmmsg = func(fd uintptr, p unsafe.Pointer, n uintptr) (uintptr, syscall.Errno) {
				if (*mmsghdr)(p).msg.Controllen != 0 {
					gsoAttempts.Add(1)
					return ^uintptr(0), errno
				}
				ordinaryAttempts.Add(1)
				return syscallSendmmsg(fd, p, n)
			}
			for repeat := 0; repeat < 2; repeat++ {
				queueGSOTestPackets(t, sender, destination.AddrPort().String(), []int{1200, 1200, 211})
				if err := sender.EndFrame(); !errors.Is(err, errno) {
					t.Fatalf("expected %v, got %v", errno, err)
				}
				if sender.gsoBuffer == nil {
					t.Fatal("unknown GSO error disabled the path")
				}
			}
			if gsoAttempts.Load() != 2 {
				t.Fatalf("GSO attempts=%d, want 2", gsoAttempts.Load())
			}
			if ordinaryAttempts.Load() != 0 {
				t.Fatalf("ordinary fallback attempts=%d, want 0", ordinaryAttempts.Load())
			}
			receiver.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
			if n, _, err := receiver.ReadFromUDP(make([]byte, 4096)); err == nil {
				t.Fatalf("unexpected datagram after %d: %d bytes", errno, n)
			}
		})
	}
}

func TestGSOUnequalShapesUseOrdinaryBatch(t *testing.T) {
	t.Setenv("NANOKVM_WEBRTC_UDP_GSO", "1")
	sender, receiver, destination := openPair(t, "udp4")
	sender.flushInterval = time.Hour
	sender.sendmmsg = func(fd uintptr, p unsafe.Pointer, n uintptr) (uintptr, syscall.Errno) {
		if (*mmsghdr)(p).msg.Controllen != 0 {
			t.Error("unequal shape used GSO")
		}
		return syscallSendmmsg(fd, p, n)
	}
	want := queueGSOTestPackets(t, sender, destination.AddrPort().String(), []int{1200, 211, 1200})
	if err := sender.EndFrame(); err != nil {
		t.Fatal(err)
	}
	got := readDatagrams(t, receiver, len(want))
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("datagram%d changed", i)
		}
	}
}

func TestGSOAcceptedBundleIsNeverResentOnError(t *testing.T) {
	t.Setenv("NANOKVM_WEBRTC_UDP_GSO", "1")
	sender, receiver, destination := openPair(t, "udp4")
	sender.flushInterval = time.Hour
	sender.sendmmsg = func(fd uintptr, p unsafe.Pointer, n uintptr) (uintptr, syscall.Errno) {
		sent, errno := syscallSendmmsg(fd, p, n)
		if errno != 0 {
			return sent, errno
		}
		return sent, syscall.EIO
	}
	want := queueGSOTestPackets(t, sender, destination.AddrPort().String(), []int{1200, 1200, 211})
	if err := sender.EndFrame(); !errors.Is(err, syscall.EIO) {
		t.Fatalf("expected EIO, got%v", err)
	}
	got := readDatagrams(t, receiver, len(want))
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("datagram%d changed", i)
		}
	}
	receiver.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	if n, _, err := receiver.ReadFromUDP(make([]byte, 4096)); err == nil {
		t.Fatalf("duplicate datagram %d bytes", n)
	}
}

func TestGSODeadlineAndCloseInterrupt(t *testing.T) {
	for _, closeSocket := range []bool{false, true} {
		t.Run(fmt.Sprint(closeSocket), func(t *testing.T) {
			t.Setenv("NANOKVM_WEBRTC_UDP_GSO", "1")
			sender, _, destination := openPair(t, "udp4")
			sender.flushInterval = time.Hour
			sender.sendmmsg = func(fd uintptr, p unsafe.Pointer, n uintptr) (uintptr, syscall.Errno) {
				return ^uintptr(0), syscall.EAGAIN
			}
			sender.SetWriteDeadline(time.Now().Add(30 * time.Millisecond))
			queueGSOTestPackets(t, sender, destination.AddrPort().String(), []int{1200, 1200})
			done := make(chan error, 1)
			go func() { done <- sender.EndFrame() }()
			if closeSocket {
				time.Sleep(3 * time.Millisecond)
				go sender.Close()
			}
			select {
			case err := <-done:
				if closeSocket {
					if !errors.Is(err, os.ErrDeadlineExceeded) && !errors.Is(err, net.ErrClosed) {
						t.Fatalf("unexpected close error%v", err)
					}
				} else if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("unexpected deadline error%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("flush did not finish")
			}
		})
	}
}
