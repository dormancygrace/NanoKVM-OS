package utils

import (
	"net"
	"testing"
)

// Per-source accounting: IPv4 by address (mapped forms included), IPv6 by
// /64, loopback exempt, a shared total, and release on close.
func TestConnLimiterSources(t *testing.T) {
	limits := newConnLimiter(5, 2)
	open := map[string]func(){}
	steps := []struct {
		op, peer string
		want     bool
	}{
		{"open", "192.0.2.1:1", true},
		{"open", "[::ffff:192.0.2.1]:2", true},
		{"open", "192.0.2.1:3", false}, // per source, mapped form counted
		{"open", "[2001:db8::1]:1", true},
		{"open", "[2001:db8::ffff]:2", true},
		{"open", "[2001:db8::2]:3", false},    // same /64
		{"open", "[2001:db8:0:1::1]:1", true}, // next /64; total 5
		{"open", "198.51.100.1:1", false},     // total
		{"open", "127.0.0.1:1", true},         // loopback: not counted
		{"open", "[::1]:1", true},
		{"close", "192.0.2.1:1", true},
		{"open", "198.51.100.1:2", true},
		{"open", "192.0.2.1:4", false}, // total again
	}
	for i, s := range steps {
		if s.op == "close" {
			open[s.peer]()
			continue
		}
		addr, err := net.ResolveTCPAddr("tcp", s.peer)
		if err != nil {
			t.Fatal(err)
		}
		source, limited := connSource(addr)
		got := !limited || limits.acquire(source)
		if got != s.want {
			t.Fatalf("step %d: open %s = %v, want %v", i, s.peer, got, s.want)
		}
		if got && limited {
			open[s.peer] = func() { limits.release(source) }
		}
	}
}
