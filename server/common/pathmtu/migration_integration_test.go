//go:build linux && integration

package pathmtu

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pion/ice/v4"
)

func TestICESelectedPairMigrationInNamespace(t *testing.T) {
	if os.Getenv("NANOKVM_PMTU_NETNS_TEST") != "1" {
		t.Skip("requires isolated namespace harness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	events := make(chan ice.PathMTUResult, 128)
	base, e := NewNet(func(event ice.PathMTUResult) {
		select {
		case events <- event:
		default:
		}
	}, false)
	if e != nil {
		t.Fatal(e)
	}
	options := func(filter func(net.IP) bool) []ice.AgentOption {
		return []ice.AgentOption{
			ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}), ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost}), ice.WithIncludeLoopback(), ice.WithInterfaceFilter(func(name string) bool { return name == "lo" }), ice.WithIPFilter(filter), ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled), ice.WithRenomination(ice.DefaultNominationValueGenerator()),
		}
	}
	left, e := ice.NewAgentWithOptions(append(options(func(ip net.IP) bool { return ip.Equal(net.ParseIP("127.0.0.1")) }), ice.WithNet(base))...)
	if e != nil {
		t.Fatal(e)
	}
	defer left.Close()
	right, e := ice.NewAgentWithOptions(options(func(ip net.IP) bool { return ip.Equal(net.ParseIP("127.0.0.1")) || ip.Equal(net.ParseIP("198.18.0.1")) })...)
	if e != nil {
		t.Fatal(e)
	}
	defer right.Close()
	l, r := connectTestICE(t, ctx, left, right)
	for {
		select {
		case event := <-events:
			if event.Confirmed && event.UDPSize == 1472 {
				goto learned
			}
		case <-ctx.Done():
			t.Fatal("initial path did not confirm full UDP budget")
		}
	}
learned:
	pair, e := left.GetSelectedCandidatePair()
	if e != nil || pair == nil {
		t.Fatalf("selected pair: %v", e)
	}
	candidates, e := right.GetLocalCandidates()
	if e != nil {
		t.Fatal(e)
	}
	var alternative ice.Candidate
	for _, c := range candidates {
		if c.Address() != pair.Remote.Address() {
			alternative = c
			break
		}
	}
	if alternative == nil {
		t.Fatal("fixture did not provide a second remote address")
	}
	transfer := func() {
		t.Helper()
		payload := bytes.Repeat([]byte{0x5a}, 1000)
		r.SetReadDeadline(time.Now().Add(2 * time.Second))
		if n, e := l.Write(payload); e != nil || n != len(payload) {
			t.Fatalf("write: %d %v", n, e)
		}
		buf := make([]byte, 2048)
		n, e := r.Read(buf)
		if e != nil || !bytes.Equal(payload, buf[:n]) {
			t.Fatalf("read: %d %v", n, e)
		}
	}
	transfer()
	route := func(mtu string) {
		t.Helper()
		if out, e := exec.Command("ip", "route", "replace", "table", "local", "local", alternative.Address()+"/32", "dev", "lo", "mtu", mtu).CombinedOutput(); e != nil {
			t.Fatalf("route: %v %s", e, out)
		}
	}
	route("1260")
	started := time.Now()
	if e = left.RenominateCandidate(pair.Local, alternative); e != nil {
		t.Fatal(e)
	}
	for {
		select {
		case event := <-events:
			if event.Remote == nil || event.Remote.Address() != alternative.Address() {
				continue
			}
			if event.Confirmed || event.UDPSize != 1232 || event.Reason != "path-reset" {
				t.Fatalf("new pair inherited old budget: %+v", event)
			}
			goto reset
		case <-ctx.Done():
			t.Fatal("new selected pair did not publish fallback")
		}
	}
reset:
	changed, e := left.GetSelectedCandidatePair()
	if e != nil || changed == nil || changed.Remote.Address() != alternative.Address() {
		t.Fatalf("selected pair did not migrate: %v", e)
	}
	transfer()
	t.Logf("selected remote address changed %s -> %s, UDP1472 reset to1232 in %v; application data delivered before and after", pair.Remote.Address(), alternative.Address(), time.Since(started))
	route("1400")
	seenReset := false
	for {
		select {
		case event := <-events:
			if event.Remote == nil || event.Remote.Address() != alternative.Address() {
				continue
			}
			if event.Reason == "path-reset" && !event.Confirmed && event.UDPSize == 1232 {
				seenReset = true
			}
			if event.Confirmed && event.UDPSize > 1232 {
				if !seenReset || event.UDPSize != 1372 {
					t.Fatalf("unsafe rediscovery: %+v", event)
				}
				transfer()
				t.Log("new route IP1400 confirmed UDP1372 after fallback; application data delivered")
				return
			}
		case <-ctx.Done():
			t.Fatal("new pair did not independently discover changed route")
		}
	}
}
