//go:build linux

package pathmtu

import (
	"net"
	"testing"

	"NanoKVM-Server/common/udpbatch"
)

func TestFrameBatchingRegistryIsBoundedAndRemovesClosedSockets(t *testing.T) {
	n, err := NewNet(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	n.EnableFrameBatching()
	conns := make([]interface{ Close() error }, 0, maxBatchConns+1)
	wrapped := 0
	for i := 0; i < maxBatchConns+1; i++ {
		conn, err := n.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
		if _, ok := conn.(*udpbatch.Conn); ok {
			wrapped++
		}
	}
	if wrapped != maxBatchConns {
		t.Fatalf("wrapped sockets=%d, want %d", wrapped, maxBatchConns)
	}
	n.BeginFrame()
	if err := n.EndFrame(); err != nil {
		t.Fatalf("empty EndFrame: %v", err)
	}
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.batchConns) != 0 {
		t.Fatalf("closed sockets retained=%d", len(n.batchConns))
	}
}
