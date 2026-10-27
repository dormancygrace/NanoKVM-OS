// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by the BSD-style LICENSE in this directory.

//go:build gc && !purego && !appengine

package websocket

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var c906API = flag.Bool("c906.api", false, "measure ReadMessage with prebuilt masked frames")

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
	c906Require(t)
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

// Account for all Go threads, including GC workers. With only thread CPU time,
// allocation-heavy ReadMessage samples can charge different shares of GC.
func c906ProcessCPUTime() int64 {
	var ts syscall.Timespec
	_, _, errno := syscall.RawSyscall(syscall.SYS_CLOCK_GETTIME, 2, uintptr(unsafe.Pointer(&ts)), 0)
	if errno != 0 {
		panic(errno)
	}
	return ts.Nano()
}

func TestC906ReadMessageMeasure(t *testing.T) {
	if !*c906API {
		t.Skip("enable -c906.api")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c906Require(t)
	// The benchmark is serial and opt-in. No other WebSocket connection runs
	// while it switches this process's selector; normal tests never mutate it.
	original := useC906Mask
	defer func() { useC906Mask = original }()
	sizes := []int{16, 64, 256, 1400, 4096, 16384, 65536, 262144}
	const passes = 16
	for rep := 0; rep < 3; rep++ {
		for j := range sizes {
			n := sizes[(j+rep*3)%len(sizes)]
			calls := c906Max(16, c906Min(8192, (2<<20)/n))
			payload := make([]byte, n)
			for i := range payload {
				payload[i] = byte(i*37 + 5)
			}
			wire := c906Frames(payload, calls)
			for variant := 0; variant < 2; variant++ {
				useC906Mask = (variant+rep+j)%2 != 0
				mode := "generic"
				if useC906Mask {
					mode = "c906"
				}
				preflight := c906Reader(wire)
				kind, last, err := preflight.ReadMessage()
				if err != nil || kind != BinaryMessage || !bytes.Equal(last, payload) {
					t.Fatal("preflight frame", n, err)
				}
				runtime.GC()
				start := time.Now()
				cpu := c906ProcessCPUTime()
				for pass := 0; pass < passes; pass++ {
					c := c906Reader(wire)
					for i := 0; i < calls; i++ {
						kind, last, err = c.ReadMessage()
						if err != nil || kind != BinaryMessage || len(last) != n {
							t.Fatal("timed frame", n, err)
						}
					}
				}
				// Include reclamation of the discarded messages in both variants.
				runtime.GC()
				cpu = c906ProcessCPUTime() - cpu
				wall := time.Since(start).Nanoseconds()
				runtime.KeepAlive(wire)
				if !bytes.Equal(last, payload) {
					t.Fatal("last frame", n)
				}
				fmt.Printf("API %s,%d,%d,%d,%d,%d\n", mode, n, rep, passes*calls, cpu, wall)
			}
		}
	}
}
