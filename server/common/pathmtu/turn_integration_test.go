//go:build linux && integration

package pathmtu

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
	"github.com/pion/turn/v5"
)

// Suppress ChannelBind success initially so the large test datagram must
// traverse Send Indication, rather than testing only the smaller channel header.
type observedTURN struct {
	net.PacketConn
	suppressBind                          atomic.Bool
	indicationData, channelData, maxOuter atomic.Int32
}

func atomicMax(v *atomic.Int32, n int) {
	for old := v.Load(); int32(n) > old; old = v.Load() {
		if v.CompareAndSwap(old, int32(n)) {
			return
		}
	}
}
func (c *observedTURN) ReadFrom(p []byte) (int, net.Addr, error) {
	n, a, e := c.PacketConn.ReadFrom(p)
	if e == nil {
		atomicMax(&c.maxOuter, n)
		if stun.IsMessage(p[:n]) {
			m := &stun.Message{Raw: append([]byte(nil), p[:n]...)}
			if m.Decode() == nil && m.Type.Method == stun.MethodSend && m.Type.Class == stun.ClassIndication {
				if data, err := m.Get(stun.AttrData); err == nil {
					atomicMax(&c.indicationData, len(data))
				}
			}
		} else if n >= 4 && p[0]&0xc0 == 0x40 {
			atomicMax(&c.channelData, n-4)
		}
	}
	return n, a, e
}
func (c *observedTURN) WriteTo(p []byte, a net.Addr) (int, error) {
	if c.suppressBind.Load() && stun.IsMessage(p) {
		m := &stun.Message{Raw: append([]byte(nil), p...)}
		if m.Decode() == nil && m.Type.Method == stun.MethodChannelBind && m.Type.Class == stun.ClassSuccessResponse {
			return len(p), nil
		}
	}
	return c.PacketConn.WriteTo(p, a)
}

func connectTestICE(t *testing.T, ctx context.Context, left, right *ice.Agent) (*ice.Conn, *ice.Conn) {
	t.Helper()
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
	lc, e := left.GetLocalCandidates()
	if e != nil {
		t.Fatal(e)
	}
	rc, e := right.GetLocalCandidates()
	if e != nil {
		t.Fatal(e)
	}
	if len(lc) == 0 || len(rc) == 0 {
		t.Fatal("missing ICE candidates")
	}
	for _, c := range lc {
		if e = right.AddRemoteCandidate(c); e != nil {
			t.Fatal(e)
		}
	}
	for _, c := range rc {
		if e = left.AddRemoteCandidate(c); e != nil {
			t.Fatal(e)
		}
	}
	lu, lp, e := left.GetLocalUserCredentials()
	if e != nil {
		t.Fatal(e)
	}
	ru, rp, e := right.GetLocalUserCredentials()
	if e != nil {
		t.Fatal(e)
	}
	type result struct {
		conn *ice.Conn
		err  error
	}
	accepted := make(chan result, 1)
	go func() { c, e := right.Accept(ctx, lu, lp); accepted <- result{c, e} }()
	l, e := left.Dial(ctx, ru, rp)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case r := <-accepted:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return l, r.conn
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return nil, nil
}

func TestTURNLocalRouteAndFramingInNamespace(t *testing.T) {
	if os.Getenv("NANOKVM_PMTU_NETNS_TEST") != "1" {
		t.Skip("requires isolated namespace harness")
	}
	ip := func(args ...string) {
		t.Helper()
		if out, e := exec.Command("ip", args...).CombinedOutput(); e != nil {
			t.Fatalf("ip: %v %s", e, out)
		}
	}
	ip("addr", "add", "198.18.0.3/32", "dev", "lo")
	ip("addr", "add", "198.18.0.4/32", "dev", "lo")
	ip("route", "replace", "table", "local", "local", "198.18.0.3/32", "dev", "lo", "mtu", "1100")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	listener, e := net.ListenPacket("udp4", "198.18.0.3:0")
	if e != nil {
		t.Fatal(e)
	}
	observed := &observedTURN{PacketConn: listener}
	observed.suppressBind.Store(true)
	server, e := turn.NewServer(turn.ServerConfig{Realm: "pmtu-test", AuthHandler: func(a *turn.RequestAttributes) (string, []byte, bool) {
		return a.Username, turn.GenerateAuthKey(a.Username, "pmtu-test", "fixture-only"), true
	}, PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: observed, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.ParseIP("198.18.0.4"), Address: "198.18.0.4"}}}})
	if e != nil {
		listener.Close()
		t.Fatal(e)
	}
	defer server.Close()
	events := make(chan ice.PathMTUResult, 16)
	base, e := NewNet(func(event ice.PathMTUResult) {
		select {
		case events <- event:
		default:
		}
	}, false)
	if e != nil {
		t.Fatal(e)
	}
	uri, e := stun.ParseURI("turn:" + listener.LocalAddr().String() + "?transport=udp")
	if e != nil {
		t.Fatal(e)
	}
	uri.Username = "fixture"
	uri.Password = "fixture-only"
	left, e := ice.NewAgent(&ice.AgentConfig{Net: base, Urls: []*stun.URI{uri}, NetworkTypes: []ice.NetworkType{ice.NetworkTypeUDP4}, CandidateTypes: []ice.CandidateType{ice.CandidateTypeRelay}, IncludeLoopback: true, MulticastDNSMode: ice.MulticastDNSModeDisabled})
	if e != nil {
		t.Fatal(e)
	}
	defer left.Close()
	right, e := ice.NewAgent(&ice.AgentConfig{NetworkTypes: []ice.NetworkType{ice.NetworkTypeUDP4}, CandidateTypes: []ice.CandidateType{ice.CandidateTypeHost}, IncludeLoopback: true, IPFilter: func(ip net.IP) bool { return ip.Equal(net.ParseIP("127.0.0.1")) }, MulticastDNSMode: ice.MulticastDNSModeDisabled})
	if e != nil {
		t.Fatal(e)
	}
	defer right.Close()
	l, r := connectTestICE(t, ctx, left, right)
	pair, e := left.GetSelectedCandidatePair()
	if e != nil || pair == nil {
		t.Fatalf("selected pair: %v", e)
	}
	relay, ok := pair.Local.(*ice.CandidateRelay)
	if !ok {
		t.Fatal("test did not select local relay")
	}
	if relay.RelayTransportAddress() != listener.LocalAddr().String() {
		t.Fatal("lost actual control endpoint metadata")
	}
	cfg := base.ICEPathMTUConfig(pair.Local, pair.Remote)
	if cfg.Probe || cfg.BaseUDP != 1008 || cfg.MaxUDP != 1008 {
		t.Fatalf("incorrect encapsulated budget: %+v", cfg)
	}
	payload := bytes.Repeat([]byte{0x5a}, cfg.MaxUDP)
	r.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, e := l.Write(payload); e != nil || n != len(payload) {
		t.Fatalf("indication send: %d %v", n, e)
	}
	buf := make([]byte, 2048)
	n, e := r.Read(buf)
	if e != nil || !bytes.Equal(buf[:n], payload) {
		t.Fatalf("indication receive: %d %v", n, e)
	}
	if observed.indicationData.Load() < int32(len(payload)) {
		t.Fatal("large datagram did not use Send Indication")
	}
	observed.suppressBind.Store(false)
	deadline := time.Now().Add(8 * time.Second)
	for observed.channelData.Load() < int32(len(payload)) && time.Now().Before(deadline) {
		if _, e = l.Write(payload); e != nil {
			t.Fatal(e)
		}
		r.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, e = r.Read(buf)
		if e != nil || !bytes.Equal(buf[:n], payload) {
			t.Fatalf("channel receive: %d %v", n, e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if observed.channelData.Load() < int32(len(payload)) {
		t.Fatal("ChannelData was not exercised")
	}
	if observed.maxOuter.Load() > 1072 {
		t.Fatalf("outer UDP exceeded MTU: %d", observed.maxOuter.Load())
	}
	// A route change must alter the config without guessing from relay allocation IP.
	ip("route", "replace", "table", "local", "local", "198.18.0.3/32", "dev", "lo", "mtu", "1000")
	smaller := base.ICEPathMTUConfig(pair.Local, pair.Remote)
	if smaller.BaseUDP != 908 || smaller.MaxUDP != 908 || smaller.RouteID == cfg.RouteID {
		t.Fatalf("smaller TURN route ignored: %+v", smaller)
	}
	// Observe the real ICE task's route reset, not only a direct config query.
	resetDeadline := time.After(3 * time.Second)
	awaitingReset := true
	for awaitingReset {
		select {
		case event := <-events:
			if event.Reason == "path-reset" && event.UDPSize == 908 && !event.Confirmed {
				awaitingReset = false
			}
		case <-resetDeadline:
			t.Fatal("ICE did not publish the smaller relay route budget")
		}
	}
	// In deployment the relay allocation is not a local routing source.
	// Temporarily remove the fixture's local allocation address to expose any
	// accidental RTA_SRC use of that address instead of the wildcard bind.
	ip("addr", "del", "198.18.0.4/32", "dev", "lo")
	nonlocalAllocation := base.ICEPathMTUConfig(pair.Local, pair.Remote)
	ip("addr", "add", "198.18.0.4/32", "dev", "lo")
	if nonlocalAllocation.MaxUDP != 908 {
		t.Fatalf("used relay allocation as local source: %+v", nonlocalAllocation)
	}
	// No room for encapsulated media must never become the zero/default budget.
	ip("route", "replace", "table", "local", "local", "198.18.0.3/32", "dev", "lo", "mtu", "92")
	empty := base.ICEPathMTUConfig(pair.Local, pair.Remote)
	if empty.BaseUDP != -1 || empty.MaxUDP != -1 {
		t.Fatalf("unsafe no-room budget: %+v", empty)
	}
	ip("route", "replace", "table", "local", "local", "198.18.0.3/32", "dev", "lo", "mtu", "1000")
	t.Logf("relay control MTU1100: inner UDP%d, indication data%d, channel data%d, max outer UDP%d; MTU1000 -> inner%d", cfg.MaxUDP, observed.indicationData.Load(), observed.channelData.Load(), observed.maxOuter.Load(), smaller.MaxUDP)
}
