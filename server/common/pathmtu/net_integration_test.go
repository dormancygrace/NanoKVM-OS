//go:build linux && integration

package pathmtu

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/transport/v4"
)

// Drop after the actual UDP socket is configured, without generating ICMP or a
// socket error. This exercises real ICE/STUN processing, not only policy calls.
type silentDropNet struct {
	*Net
	limit   atomic.Int32
	dropped atomic.Int32
}
type silentDropUDP struct {
	transport.UDPConn
	owner *silentDropNet
}

func (n *silentDropNet) ListenUDP(network string, addr *net.UDPAddr) (transport.UDPConn, error) {
	c, err := n.Net.ListenUDP(network, addr)
	if err != nil {
		return nil, err
	}
	return &silentDropUDP{c, n}, nil
}
func (c *silentDropUDP) WriteTo(p []byte, a net.Addr) (int, error) {
	if limit := c.owner.limit.Load(); limit > 0 && len(p) > int(limit) {
		c.owner.dropped.Add(1)
		return len(p), nil
	}
	return c.UDPConn.WriteTo(p, a)
}

func TestICEPMTUOverUDPSilentDrop(t *testing.T) {
	for _, network := range []ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6} {
		t.Run(network.String(), func(t *testing.T) {
			if network == ice.NetworkTypeUDP6 && os.Getenv("NANOKVM_PMTU_NETNS_TEST") != "1" {
				t.Skip("IPv6 ICE needs the isolated ULA namespace harness; upstream excludes ::1 candidates")
			}
			t.Parallel()
			testICEPMTUSilentDrop(t, network)
		})
	}
}

func testICEPMTUSilentDrop(t *testing.T, network ice.NetworkType) {
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	events := make(chan ice.PathMTUResult, 128)
	base, err := NewNet(func(e ice.PathMTUResult) {
		select {
		case events <- e:
		default:
		}
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	dropped := &silentDropNet{Net: base}
	ceiling := int32(1352)
	if network == ice.NetworkTypeUDP6 {
		ceiling = 1392
	}
	dropped.limit.Store(ceiling)
	makeAgent := func(n transport.Net) *ice.Agent {
		a, e := ice.NewAgent(&ice.AgentConfig{Net: n, NetworkTypes: []ice.NetworkType{network}, CandidateTypes: []ice.CandidateType{ice.CandidateTypeHost}, IncludeLoopback: true, InterfaceFilter: func(name string) bool { return name == "lo" }, MulticastDNSMode: ice.MulticastDNSModeDisabled})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { a.Close() })
		return a
	}
	left, right := makeAgent(dropped), makeAgent(nil)
	for _, a := range []*ice.Agent{left, right} {
		done := make(chan struct{})
		if err := a.OnCandidate(func(c ice.Candidate) {
			if c == nil {
				close(done)
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := a.GatherCandidates(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	lc, err := left.GetLocalCandidates()
	if err != nil {
		t.Fatal(err)
	}
	rc, err := right.GetLocalCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(lc) == 0 || len(rc) == 0 {
		t.Fatal("no candidates for requested address family")
	}
	for _, c := range lc {
		if err := right.AddRemoteCandidate(c); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range rc {
		if err := left.AddRemoteCandidate(c); err != nil {
			t.Fatal(err)
		}
	}
	lu, lp, err := left.GetLocalUserCredentials()
	if err != nil {
		t.Fatal(err)
	}
	ru, rp, err := right.GetLocalUserCredentials()
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan error, 2)
	go func() { _, e := left.Dial(ctx, ru, rp); connected <- e }()
	go func() { _, e := right.Accept(ctx, lu, lp); connected <- e }()
	for i := 0; i < 2; i++ {
		select {
		case e := <-connected:
			if e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	stage := 0
	seenBase := false
	for {
		select {
		case e := <-events:
			if e.Local != nil && e.Local.NetworkType() != network {
				t.Fatalf("cross-connection event: %+v", e)
			}
			if !e.Confirmed && e.UDPSize == 1232 && e.Reason == "path-reset" {
				seenBase = true
			}
			if stage == 0 && e.Confirmed && e.UDPSize > 1232 {
				if !seenBase {
					t.Fatal("increased without publishing initial fallback")
				}
				if e.UDPSize > int(ceiling) {
					t.Fatalf("accepted dropped size %d", e.UDPSize)
				}
				t.Logf("authenticated UDP delivery confirmed %d after %d silent drops", e.UDPSize, dropped.dropped.Load())
				stage = 1
				dropped.limit.Store(1232)
			} else if stage == 1 && !e.Confirmed && e.UDPSize == 1232 && e.Reason == "probe-timeout" {
				t.Logf("revalidation detected silent loss and restored base after %d drops", dropped.dropped.Load())
				return
			}
		case <-ctx.Done():
			t.Fatalf("stage %d: %v", stage, ctx.Err())
		}
	}
}
