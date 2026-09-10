//go:build linux && (amd64 || arm64 || riscv64)

package udpfast

import (
	"bytes"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

func TestDatagramsAndDeadlines(t *testing.T) {
	for _, network := range []string{"udp4", "udp6"} {
		t.Run(network, func(t *testing.T) {
			ip := net.IPv4(127, 0, 0, 1)
			if network == "udp6" {
				ip = net.IPv6loopback
			}
			receiver, err := net.ListenUDP(network, &net.UDPAddr{IP: ip})
			if err != nil {
				t.Fatal(err)
			}
			defer receiver.Close()
			sender, err := net.ListenUDP(network, &net.UDPAddr{IP: ip})
			if err != nil {
				t.Fatal(err)
			}
			wrapped := Wrap(sender)
			defer wrapped.Close()
			if _, ok := wrapped.(*udpConn); !ok {
				t.Fatal("fast path not selected")
			}
			for _, payload := range [][]byte{nil, []byte("hello"), bytes.Repeat([]byte{0xa7}, 1200)} {
				n, err := wrapped.WriteTo(payload, receiver.LocalAddr())
				if err != nil || n != len(payload) {
					t.Fatalf("send %d %v", n, err)
				}
				receiver.SetReadDeadline(time.Now().Add(time.Second))
				buf := make([]byte, 1500)
				n, _, err = receiver.ReadFromUDP(buf)
				if err != nil || !bytes.Equal(buf[:n], payload) {
					t.Fatalf("receive %d %v", n, err)
				}
			}
			wrapped.SetWriteDeadline(time.Now().Add(-time.Second))
			if _, err := wrapped.WriteTo([]byte("expired"), receiver.LocalAddr()); !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("deadline: %v", err)
			}
			wrapped.SetWriteDeadline(time.Time{})
			if _, err := wrapped.WriteTo([]byte("resumed"), receiver.LocalAddr()); err != nil {
				t.Fatal(err)
			}
			wrapped.Close()
			if _, err := wrapped.WriteTo([]byte("closed"), receiver.LocalAddr()); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("close: %v", err)
			}
		})
	}
}

func TestConcurrentPackets(t *testing.T) {
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	receiver.SetReadBuffer(1 << 20)
	receiver.SetReadDeadline(time.Now().Add(5 * time.Second))
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := Wrap(sender)
	defer wrapped.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id byte) {
			defer wg.Done()
			for j := 0; j < 16; j++ {
				packet := bytes.Repeat([]byte{id}, 1200)
				packet[1] = byte(j)
				if _, err := wrapped.WriteToUDP(packet, receiver.LocalAddr().(*net.UDPAddr)); err != nil {
					t.Error(err)
					return
				}
			}
		}(byte(i))
	}
	seen := map[[2]byte]bool{}
	for i := 0; i < 128; i++ {
		packet := make([]byte, 1500)
		n, _, err := receiver.ReadFromUDP(packet)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1200 || !bytes.Equal(packet[2:n], bytes.Repeat(packet[:1], 1198)) {
			t.Fatal("datagram corrupted")
		}
		key := [2]byte{packet[0], packet[1]}
		if seen[key] {
			t.Fatal("duplicate")
		}
		seen[key] = true
	}
	wg.Wait()
}
