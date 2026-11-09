// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by the BSD-style LICENSE in this directory.

//go:build gc && !purego && !appengine

package websocket

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

type c906MemoryConn struct{ *bytes.Reader }

func (c *c906MemoryConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *c906MemoryConn) Close() error                     { return nil }
func (c *c906MemoryConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *c906MemoryConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *c906MemoryConn) SetDeadline(time.Time) error      { return nil }
func (c *c906MemoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *c906MemoryConn) SetWriteDeadline(time.Time) error { return nil }

func c906Frames(payload []byte, count int) []byte {
	header := []byte{0x82, 0x80}
	switch n := len(payload); {
	case n < 126:
		header[1] |= byte(n)
	case n <= 65535:
		header[1] |= 126
		header = binary.BigEndian.AppendUint16(header, uint16(n))
	default:
		header[1] |= 127
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	wire := make([]byte, 0, (len(payload)+len(header)+4)*count)
	for i := 0; i < count; i++ {
		key := [4]byte{byte(i*17 + 1), 0x82, byte(i >> 8), 0xcf}
		wire = append(wire, header...)
		wire = append(wire, key[:]...)
		for j, v := range payload {
			wire = append(wire, v^key[j&3])
		}
	}
	return wire
}
func c906Reader(wire []byte) *Conn {
	return newConn(&c906MemoryConn{bytes.NewReader(wire)}, true, 4096, 4096, nil, nil, nil)
}
func TestC906ReadMessage(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 16, 125, 126, 127, 1400, 4096, 16384, 65535, 65536, 262144} {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte(i*37 + 5)
		}
		c := c906Reader(c906Frames(payload, 4))
		for i := 0; i < 4; i++ {
			kind, data, err := c.ReadMessage()
			if err != nil || kind != BinaryMessage || !bytes.Equal(data, payload) {
				t.Fatalf("frame n=%d i=%d type=%d err=%v", n, i, kind, err)
			}
		}
	}
}
