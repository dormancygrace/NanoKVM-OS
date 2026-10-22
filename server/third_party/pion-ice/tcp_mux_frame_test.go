// SPDX-License-Identifier: MIT
// NanoKVM addition: the length limit of ICE-TCP framing (RFC 4571).
package ice

import (
	"bytes"
	"errors"
	"math"
	"net"
	"testing"
	"time"
)

// A packet longer than the 16-bit RFC 4571 length is refused, not framed
// with a truncated length.
func TestWriteStreamingPacketLength(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	// A missing size guard must fail promptly instead of hanging on net.Pipe.
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	if n, err := writeStreamingPacket(client, make([]byte, math.MaxUint16+1)); !errors.Is(err, errStreamingPacketTooLarge) || n != 0 {
		t.Fatalf("oversized packet: %d, %v", n, err)
	}

	largest := make([]byte, math.MaxUint16)
	largest[len(largest)-1] = 0x5a
	read := make(chan error, 1)
	buf := make([]byte, math.MaxUint16)
	go func() {
		n, err := readStreamingPacket(server, buf)
		if err == nil && (n != len(largest) || !bytes.Equal(buf[:n], largest)) {
			err = errors.New("wrong packet")
		}
		read <- err
	}()
	if n, err := writeStreamingPacket(client, largest); err != nil || n != len(largest) {
		t.Fatalf("largest packet: %d, %v", n, err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
}
