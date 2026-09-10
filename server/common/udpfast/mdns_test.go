//go:build linux && (amd64 || arm64 || riscv64)

package udpfast

import (
	"errors"
	"github.com/pion/transport/v4"
	"golang.org/x/net/ipv4"
	"net"
	"os"
	"testing"
	"time"
)

// The test listener binds an ephemeral loopback port so the test cannot
// interfere with the host's real mDNS service on port 5353.
type mdnsListener struct{ transport.Net }

func (mdnsListener) ListenUDP(network string, _ *net.UDPAddr) (transport.UDPConn, error) {
	return net.ListenUDP(network, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
}

func TestMDNSReadPreservesSocketType(t *testing.T) {
	for _, addr := range []*net.UDPAddr{{IP: net.IPv4(224, 0, 0, 251)}, {Port: 5353}} {
		network := &Net{Net: mdnsListener{}}
		conn, err := network.ListenUDP("udp4", addr)
		if err != nil {
			t.Fatal(err)
		}
		packet := ipv4.NewPacketConn(conn)
		packet.SetReadDeadline(time.Now().Add(-time.Second))
		_, _, _, err = packet.ReadFrom(make([]byte, 1200))
		conn.Close()
		// The broken adapter returned "invalid connection type" immediately here,
		// causing Pion mDNS to loop instead of waiting for data or a deadline.
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("mDNS read failed outside polling: %v", err)
		}
	}
}
