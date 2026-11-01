//go:build linux && (amd64 || arm64 || riscv64)

package udpfast

import (
	"bytes"
	"errors"
	"net"
	"os"
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
