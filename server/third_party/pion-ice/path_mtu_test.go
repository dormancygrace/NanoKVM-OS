package ice

import (
	"github.com/pion/logging"
	"github.com/pion/stun/v4"
	"github.com/pion/transport/v4"
	"net/netip"
	"testing"
	"time"
)

type mtuTestNet struct {
	transport.Net
	config PathMTUConfig
	events []PathMTUResult
}

func (n *mtuTestNet) ICEPathMTUConfig(_, _ Candidate) PathMTUConfig { return n.config }
func (n *mtuTestNet) OnICEPathMTU(e PathMTUResult)                  { n.events = append(n.events, e) }

func mtuFixture(t *testing.T) (*Agent, *mtuTestNet, *CandidatePair) {
	t.Helper()
	n := &mtuTestNet{config: PathMTUConfig{BaseUDP: 1232, MaxUDP: 1472, Probe: true, RouteID: "route1"}}
	local, err := NewCandidateHost(&CandidateHostConfig{Network: "udp", Address: "127.0.0.1", Port: 9000, Component: 1})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := NewCandidateHost(&CandidateHostConfig{Network: "udp", Address: "127.0.0.1", Port: 9001, Component: 1})
	if err != nil {
		t.Fatal(err)
	}
	pair := newCandidatePair(local, remote, true)
	a := &Agent{net: n, localUfrag: "local", remoteUfrag: "remote", remotePwd: "test-password", log: logging.NewDefaultLoggerFactory().NewLogger("pmtu-test")}
	a.selectedPair.Store(pair)
	a.isControlling.Store(true)
	a.resetPathMTU(pair, time.Now())
	return a, n, pair
}

func armProbe(t *testing.T, a *Agent, size int) *stun.Message {
	t.Helper()
	msg, err := a.makePathMTUProbe(size)
	if err != nil {
		t.Fatal(err)
	}
	p := a.pathMTU
	p.target = size
	p.pending = true
	p.sent = time.Now()
	p.transaction = msg.TransactionID
	reply, err := stun.Build(stun.BindingSuccess, msg, stun.NewShortTermIntegrity(a.remotePwd), stun.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestPathMTUProbeSizeAndIntegrity(t *testing.T) {
	a, _, _ := mtuFixture(t)
	for _, size := range []int{1232, 1280, 1352, 1452, 1472} {
		m, err := a.makePathMTUProbe(size)
		if err != nil || len(m.Raw) != size {
			t.Fatalf("%d: size %d %v", size, len(m.Raw), err)
		}
		if err := stun.MessageIntegrity([]byte(a.remotePwd)).Check(m); err != nil {
			t.Fatal(err)
		}
		if err := stun.Fingerprint.Check(m); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, attr := range m.Attributes {
			if attr.Type == stun.AttrSoftware {
				count++
				if len(attr.Value) >= 128 {
					t.Fatal("oversized SOFTWARE")
				}
			}
		}
		if count < 2 {
			t.Fatal("expected padding attributes")
		}
		m.Raw[100] ^= 1
		if err := stun.MessageIntegrity([]byte(a.remotePwd)).Check(m); err == nil {
			t.Fatal("unprotected probe bytes")
		}
	}
}

func TestPathMTURequiresAuthenticatedMatchingAcknowledgements(t *testing.T) {
	a, n, pair := mtuFixture(t)
	source := pair.Remote.addrPort()
	good := armProbe(t, a, 1472)
	bad, err := stun.Build(stun.BindingSuccess, good, stun.NewShortTermIntegrity("wrong"), stun.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if a.handleInboundResponse(pair.Remote, pair.Local, source, bad) {
		t.Fatal("accepted invalid integrity")
	}
	if a.pathMTU.current != 1232 {
		t.Fatal("invalid integrity increased MTU")
	}
	if !a.handlePathMTUResponse(good, pair.Local, pair.Remote, netip.MustParseAddrPort("127.0.0.1:9002"), time.Now()) {
		t.Fatal("probe not recognized")
	}
	if !a.pathMTU.pending || a.pathMTU.current != 1232 {
		t.Fatal("wrong address acknowledged probe")
	}
	if !a.handleInboundResponse(pair.Remote, pair.Local, source, good) {
		t.Fatal("valid reply rejected")
	}
	if a.pathMTU.current != 1232 {
		t.Fatal("increased after only one confirmation")
	}
	good = armProbe(t, a, 1472)
	a.handleInboundResponse(pair.Remote, pair.Local, source, good)
	if a.pathMTU.current != 1472 || !a.pathMTU.confirmed {
		t.Fatal("two valid replies did not confirm size")
	}
	n.config.RouteID = "route2"
	a.resetPathMTU(pair, time.Now())
	if a.pathMTU.current != 1232 || a.pathMTU.confirmed {
		t.Fatal("route reset retained learned size")
	}
	if a.handlePathMTUResponse(good, pair.Local, pair.Remote, source, time.Now()) {
		t.Fatal("stale reply recognized after reset")
	}
}

func TestPathMTUSilentLossSearchAndRevalidation(t *testing.T) {
	a, _, pair := mtuFixture(t)
	// Policy simulation: all datagrams over 1352 disappear without any ICMP.
	for steps := 0; steps < 160; steps++ {
		p := a.pathMTU
		if p.target <= 1352 {
			reply := armProbe(t, a, p.target)
			a.handleInboundResponse(pair.Remote, pair.Local, pair.Remote.addrPort(), reply)
		} else {
			a.pathMTUFailure(time.Now())
		}
		if p.current == 1352 && p.target == p.current && p.upper == p.current {
			break
		}
	}
	if a.pathMTU.current != 1352 {
		t.Fatalf("discovered %d, want 1352", a.pathMTU.current)
	}
	a.pathMTU.target = a.pathMTU.current
	for i := 0; i < 3; i++ {
		a.pathMTUFailure(time.Now())
	}
	if a.pathMTU.current != 1232 || a.pathMTU.confirmed {
		t.Fatal("black-hole fallback not applied")
	}
}

func TestPathMTUSmallerKnownLimit(t *testing.T) {
	a, n, pair := mtuFixture(t)
	n.config.MaxUDP = 1152
	a.resetPathMTU(pair, time.Now())
	if a.pathMTU.current != 1152 || a.pathMTU.target != 1152 {
		t.Fatal("ignored smaller route limit")
	}
}

func TestPathMTUTinyKnownLimitNeverEnlarged(t *testing.T) {
	c := normalizePathMTUConfig(PathMTUConfig{BaseUDP: 1232, MaxUDP: 128, Probe: true})
	if c.BaseUDP != 128 || c.MaxUDP != 128 || c.Probe {
		t.Fatalf("unsafe tiny budget: %+v", c)
	}
}
