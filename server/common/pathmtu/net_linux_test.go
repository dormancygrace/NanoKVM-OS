//go:build linux

package pathmtu

import (
	"net"
	"testing"

	"github.com/pion/ice/v4"
	"golang.org/x/sys/unix"
)

func TestRouteAndNoFragmentSocket(t *testing.T) {
	for _, tc := range []struct {
		network, address      string
		level, option, maxUDP int
	}{
		{"udp4", "127.0.0.1", unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, 1472},
		{"udp6", "::1", unix.IPPROTO_IPV6, unix.IPV6_MTU_DISCOVER, 1452},
	} {
		t.Run(tc.network, func(t *testing.T) {
			n, err := NewNet(nil, false)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := n.ListenUDP(tc.network, &net.UDPAddr{IP: net.ParseIP(tc.address)})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			udp := conn.(*net.UDPConn)
			raw, err := udp.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var value int
			var optionErr error
			if err = raw.Control(func(fd uintptr) {
				value, optionErr = unix.GetsockoptInt(int(fd), tc.level, tc.option)
			}); err != nil {
				t.Fatal(err)
			}
			if optionErr != nil {
				t.Fatal(optionErr)
			}
			if value != unix.IP_PMTUDISC_DO {
				t.Fatalf("fragmentation not disabled: %d", value)
			}
			local, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: tc.address, Port: udp.LocalAddr().(*net.UDPAddr).Port, Component: 1})
			if err != nil {
				t.Fatal(err)
			}
			remote, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: tc.address, Port: 9001, Component: 1})
			if err != nil {
				t.Fatal(err)
			}
			config := n.ICEPathMTUConfig(local, remote)
			if !config.Probe || config.BaseUDP != 1232 || config.MaxUDP != tc.maxUDP {
				t.Fatalf("bad route config %+v", config)
			}
		})
	}
}

// Measures the production route lookup/configuration path, not all ICE overhead.
func BenchmarkRouteConfigIPv4(b *testing.B) {
	n, err := NewNet(nil, false)
	if err != nil {
		b.Fatal(err)
	}
	c, err := n.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	local, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: "127.0.0.1", Port: c.LocalAddr().(*net.UDPAddr).Port, Component: 1})
	if err != nil {
		b.Fatal(err)
	}
	remote, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: "127.0.0.1", Port: 9001, Component: 1})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg := n.ICEPathMTUConfig(local, remote)
		if !cfg.Probe || cfg.MaxUDP != 1472 {
			b.Fatalf("bad route %+v", cfg)
		}
	}
}

func TestListenPacketNoFragment(t *testing.T) {
	n, e := NewNet(nil, false)
	if e != nil {
		t.Fatal(e)
	}
	c, e := n.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	udp, ok := c.(*net.UDPConn)
	if !ok {
		t.Fatal("unexpected TURN socket type")
	}
	raw, e := udp.SyscallConn()
	if e != nil {
		t.Fatal(e)
	}
	var mode int
	var sockErr error
	if e = raw.Control(func(fd uintptr) { mode, sockErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER) }); e != nil {
		t.Fatal(e)
	}
	if sockErr != nil || mode != unix.IP_PMTUDISC_DO {
		t.Fatalf("TURN UDP fragmentation policy: %d %v", mode, sockErr)
	}
}
