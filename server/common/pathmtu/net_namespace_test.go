//go:build linux && integration

package pathmtu

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/pion/ice/v4"
)

// Run only through scripts/test-pmtu-netns.sh, in its disposable network namespace.
func TestRouteBudgetChangesInNamespace(t *testing.T) {
	if os.Getenv("NANOKVM_PMTU_NETNS_TEST") != "1" {
		t.Skip("requires isolated namespace harness")
	}
	ip := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v: %s", args, err, out)
		}
	}
	for _, tc := range []struct {
		network, src, dst, prefix string
		small, smallUDP, largeUDP int
	}{
		{"udp4", "198.18.0.1", "198.18.0.2", "198.18.0.2/32", 1200, 1172, 1372},
		{"udp6", "fd77:706d:7475::1", "fd77:706d:7475::2", "fd77:706d:7475::2/128", 1280, 1232, 1352},
	} {
		t.Run(tc.network, func(t *testing.T) {
			n, err := NewNet(nil, false)
			if err != nil {
				t.Fatal(err)
			}
			c, err := n.ListenUDP(tc.network, &net.UDPAddr{IP: net.ParseIP(tc.src)})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			local, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: tc.src, Port: c.LocalAddr().(*net.UDPAddr).Port, Component: 1})
			if err != nil {
				t.Fatal(err)
			}
			remote, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: tc.dst, Port: 9001, Component: 1})
			if err != nil {
				t.Fatal(err)
			}
			family := "-4"
			if tc.network == "udp6" {
				family = "-6"
			}
			ip(family, "route", "replace", tc.prefix, "dev", "lo", "mtu", strconv.Itoa(tc.small))
			small := n.ICEPathMTUConfig(local, remote)
			if !small.Probe || small.BaseUDP != tc.smallUDP || small.MaxUDP != tc.smallUDP {
				t.Fatalf("small route ignored: %+v", small)
			}
			ip(family, "route", "replace", tc.prefix, "dev", "lo", "mtu", "1400")
			large := n.ICEPathMTUConfig(local, remote)
			if !large.Probe || large.BaseUDP != 1232 || large.MaxUDP != tc.largeUDP || small.RouteID == large.RouteID {
				t.Fatalf("route change ignored: before=%+v after=%+v", small, large)
			}
			t.Logf("actual route MTU %d -> 1400: UDP %d -> %d; base %d -> %d", tc.small, small.MaxUDP, large.MaxUDP, small.BaseUDP, large.BaseUDP)
		})
	}
}

func TestRemoteRelaySmallerRouteInNamespace(t *testing.T) {
	if os.Getenv("NANOKVM_PMTU_NETNS_TEST") != "1" {
		t.Skip("requires isolated namespace harness")
	}
	if out, err := exec.Command("ip", "-4", "route", "replace", "198.18.0.2/32", "dev", "lo", "mtu", "1100").CombinedOutput(); err != nil {
		t.Fatalf("route: %v %s", err, out)
	}
	n, err := NewNet(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	c, err := n.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("198.18.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	local, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: "198.18.0.1", Port: c.LocalAddr().(*net.UDPAddr).Port, Component: 1})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := ice.NewCandidateRelay(&ice.CandidateRelayConfig{Network: "udp", Address: "198.18.0.2", Port: 9001, Component: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := n.ICEPathMTUConfig(local, relay)
	if cfg.Probe || cfg.BaseUDP != 1072 || cfg.MaxUDP != 1072 {
		t.Fatalf("remote relay ignored smaller first-hop route: %+v", cfg)
	}
}
