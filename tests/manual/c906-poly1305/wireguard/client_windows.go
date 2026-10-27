package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"io"
	"net"
	"net/netip"
	"os"
	"slices"
	"time"
)

type Config struct{ Mode, PrivateKey, PublicKey string }

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
func main() {
	var cfg Config
	must(json.NewDecoder(os.Stdin).Decode(&cfg))
	addr := "192.168.1.20:34908"
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	if cfg.Mode == "wireguard" {
		tun, tnet, e := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("198.18.90.1")}, nil, 1420)
		must(e)
		dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, ""))
		defer dev.Close()
		must(dev.IpcSet("private_key=" + cfg.PrivateKey + "\npublic_key=" + cfg.PublicKey + "\nallowed_ip=198.18.90.2/32\nendpoint=192.168.1.20:34907\n"))
		must(dev.Up())
		addr = "198.18.90.2:34908"
		dial = tnet.DialContext
	} else if cfg.Mode != "direct" {
		panic("mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, e := dial(ctx, "tcp4", addr)
	must(e)
	defer c.Close()
	if x, ok := c.(*net.TCPConn); ok {
		must(x.SetNoDelay(true))
	}
	u, e := dial(ctx, "udp4", addr)
	must(e)
	defer u.Close()
	type C struct{ n, count, mode int }
	cases := []C{}
	for _, n := range []int{8, 16, 32, 64, 128, 512, 1200, 1392} {
		cases = append(cases, C{n, 256, 2})
	}
	cases = append(cases, C{32768, 1024, 0}, C{32768, 1024, 1})
	for _, test := range cases {
		n, count, mode := test.n, test.count, test.mode
		p, buf := data(n), make([]byte, n+1)
		must(c.SetDeadline(time.Now().Add(60 * time.Second)))
		var h [12]byte
		binary.BigEndian.PutUint32(h[:4], uint32(n))
		binary.BigEndian.PutUint32(h[4:8], uint32(count))
		binary.BigEndian.PutUint32(h[8:], uint32(mode))
		write(c, h[:])
		var ready [1]byte
		read(c, ready[:])
		if ready[0] != 0x90 {
			panic("ready")
		}
		times := make([]int64, 0, count)
		start := time.Now()
		for i := 0; i < count; i++ {
			one := time.Now()
			if mode == 2 {
				binary.BigEndian.PutUint64(p, uint64(i))
				must(u.SetDeadline(time.Now().Add(3 * time.Second)))
				write(u, p)
				nr, e := u.Read(buf)
				must(e)
				if nr != n || !bytes.Equal(buf[:n], p) {
					panic("UDP echo mismatch")
				}
				times = append(times, time.Since(one).Nanoseconds())
			} else if mode == 0 {
				write(c, p)
			} else {
				read(c, buf[:n])
				if !bytes.Equal(buf[:n], p) {
					panic("TCP download mismatch")
				}
			}
		}
		elapsed := time.Since(start)
		var stats map[string]any
		must(json.NewDecoder(c).Decode(&stats))
		if stats["payload_ok"] != true || int(stats["bytes"].(float64)) != n || int(stats["calls"].(float64)) != count {
			panic("stats mismatch")
		}
		stats["transport"] = cfg.Mode
		stats["client_wall_ns"] = elapsed.Nanoseconds()
		if mode == 2 {
			slices.Sort(times)
			stats["rtt_median_ns"] = times[len(times)/2]
			stats["rtt_p95_ns"] = times[len(times)*95/100]
		}
		must(json.NewEncoder(os.Stdout).Encode(stats))
	}
	write(c, make([]byte, 12))
	fmt.Fprintln(os.Stderr, "CLIENT_COMPLETE")
}
