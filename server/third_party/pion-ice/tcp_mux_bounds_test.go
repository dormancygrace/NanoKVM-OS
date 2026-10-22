// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package ice

import (
	"encoding/binary"
	"net"
	"testing"
)

type framingConn struct {
	net.Conn
	data   []byte
	writes int
}

func (c *framingConn) Write(p []byte) (int, error) {
	c.writes++
	c.data = append(c.data, p...)
	return len(p), nil
}

func TestWriteStreamingPacketLengthBounds(t *testing.T) {
	for _, size := range []int{0, 1, 65535, 65536} {
		conn := &framingConn{}
		n, err := writeStreamingPacket(conn, make([]byte, size))
		if size > 65535 {
			if err == nil || n != 0 || conn.writes != 0 {
				t.Fatalf("oversized frame: n=%d err=%v writes=%d", n, err, conn.writes)
			}
			continue
		}
		if err != nil || n != size || len(conn.data) != size+2 || int(binary.BigEndian.Uint16(conn.data)) != size {
			t.Fatalf("size=%d: n=%d err=%v frame=%d", size, n, err, len(conn.data))
		}
	}
}
