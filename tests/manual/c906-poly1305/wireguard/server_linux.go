package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func data(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i*37 + n*11)
	}
	return p
}
func write(c net.Conn, p []byte) {
	n, e := c.Write(p)
	must(e)
	if n != len(p) {
		panic("short write")
	}
}
func read(c net.Conn, p []byte) { _, e := io.ReadFull(c, p); must(e) }
func cpu() (uint64, uint64) {
	var r syscall.Rusage
	must(syscall.Getrusage(syscall.RUSAGE_SELF, &r))
	return uint64(r.Utime.Nano()), uint64(r.Stime.Nano())
}
func ticks() []uint64 {
	p, e := os.ReadFile("/proc/stat")
	must(e)
	f := strings.Fields(strings.SplitN(string(p), "\n", 2)[0])
	v := make([]uint64, 8)
	for i := range v {
		v[i], e = strconv.ParseUint(f[i+1], 10, 64)
		must(e)
	}
	return v
}
func main() {
	addr := os.Args[1]
	ln, e := net.Listen("tcp4", addr)
	must(e)
	defer ln.Close()
	udpaddr, e := net.ResolveUDPAddr("udp4", addr)
	must(e)
	u, e := net.ListenUDP("udp4", udpaddr)
	must(e)
	defer u.Close()
	fmt.Printf("{\"ready\":true,\"pid\":%d}\n", os.Getpid())
	must(ln.(*net.TCPListener).SetDeadline(time.Now().Add(20 * time.Second)))
	c, e := ln.Accept()
	must(e)
	defer c.Close()
	must(c.(*net.TCPConn).SetNoDelay(true))
	for {
		must(c.SetDeadline(time.Now().Add(60 * time.Second)))
		var h [12]byte
		read(c, h[:])
		n, count, mode := int(binary.BigEndian.Uint32(h[:4])), int(binary.BigEndian.Uint32(h[4:8])), int(binary.BigEndian.Uint32(h[8:]))
		if n == 0 {
			break
		}
		if n > 32768 || count < 1 || count > 2048 || mode > 2 {
			panic("bad command")
		}
		p, buf := data(n), make([]byte, n+1)
		write(c, []byte{0x90})
		ts0 := ticks()
		u0, s0 := cpu()
		start := time.Now()
		for i := 0; i < count; i++ {
			if mode == 2 {
				must(u.SetReadDeadline(time.Now().Add(3 * time.Second)))
				nr, peer, e := u.ReadFromUDP(buf)
				must(e)
				binary.BigEndian.PutUint64(p, uint64(i))
				if nr != n || !bytes.Equal(p, buf[:n]) {
					panic("UDP payload/order mismatch")
				}
				nw, e := u.WriteToUDP(buf[:n], peer)
				must(e)
				if nw != n {
					panic("UDP short write")
				}
			} else if mode == 0 {
				read(c, buf[:n])
				if !bytes.Equal(p, buf[:n]) {
					panic("TCP upload mismatch")
				}
			} else {
				write(c, p)
			}
		}
		elapsed := time.Since(start)
		u1, s1 := cpu()
		ts1 := ticks()
		for i := range ts1 {
			ts1[i] -= ts0[i]
		}
		must(json.NewEncoder(c).Encode(map[string]any{"mode": mode, "bytes": n, "calls": count, "server_user_ns": u1 - u0, "server_sys_ns": s1 - s0, "server_wall_ns": elapsed.Nanoseconds(), "system_ticks": ts1, "payload_ok": true}))
	}
	fmt.Println("WG_BENCH_COMPLETE")
}
