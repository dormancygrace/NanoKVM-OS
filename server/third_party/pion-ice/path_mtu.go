// SPDX-License-Identifier: MIT
// NanoKVM addition: opt-in datagram PLPMTU probing on the selected ICE flow.
package ice

import (
	"bytes"
	"fmt"
	"github.com/pion/stun/v4"
	"net/netip"
	"time"
)

// PathMTUConfig describes UDP payload budgets, not IP packet sizes.
// Probe may be true only when the transport guarantees no IP fragmentation.
// RouteID must change when an underlay route changes without an ICE change.
type PathMTUConfig struct {
	BaseUDP, MaxUDP int
	Probe           bool
	RouteID         string
}

// PathMTUResult is delivered on the serialized ICE task loop. The callback must
// not block or call Agent methods. A reset is reported before any larger probe.
type PathMTUResult struct {
	Local, Remote Candidate
	UDPSize       int
	Confirmed     bool
	Reason        string
}

// PathMTUObserver optionally enables discovery for a transport.Net implementation.
// Config must account for additional encapsulation and known smaller limits.
type PathMTUObserver interface {
	ICEPathMTUConfig(local, remote Candidate) PathMTUConfig
	OnICEPathMTU(PathMTUResult)
}

type pathMTUState struct {
	pair                   *CandidatePair
	config                 PathMTUConfig
	current, upper, target int
	resumeSearch           int
	confirmed              bool
	pending                bool
	transaction            [stun.TransactionIDSize]byte
	sent, next             time.Time
	failures, successes    int
}

func normalizePathMTUConfig(c PathMTUConfig) PathMTUConfig {
	if c.BaseUDP == 0 {
		c.BaseUDP = 1232
	} // RTP 1216 + largest SRTP tag 16.
	if c.MaxUDP == 0 {
		c.MaxUDP = c.BaseUDP
	}
	// Bound memory and probe amplification even if a caller supplies bad input.
	if c.MaxUDP > 2048 {
		c.MaxUDP = 2048
	}
	if c.MaxUDP < 0 {
		c.MaxUDP = 0
	}
	c.MaxUDP &^= 3
	if c.BaseUDP > c.MaxUDP {
		c.BaseUDP = c.MaxUDP
	}
	if c.BaseUDP < 0 {
		c.BaseUDP = 0
	}
	// An authenticated ICE request may not fit unusually small limits. Keep
	// the smaller media ceiling; never enlarge it merely to fit a probe.
	if c.BaseUDP < 256 {
		c.Probe = false
	}
	c.BaseUDP &^= 3
	return c
}

func (a *Agent) publishPathMTU(reason string) {
	p := a.pathMTU
	if p == nil {
		return
	}
	if observer, ok := a.net.(PathMTUObserver); ok {
		observer.OnICEPathMTU(PathMTUResult{p.pair.Local, p.pair.Remote, p.current, p.confirmed, reason})
	}
}

func (a *Agent) resetPathMTU(pair *CandidatePair, now time.Time) {
	observer, ok := a.net.(PathMTUObserver)
	if !ok {
		return
	}
	a.pathMTU = nil
	if pair == nil {
		observer.OnICEPathMTU(PathMTUResult{UDPSize: 1232, Reason: "path-removed"})
		return
	}
	config := normalizePathMTUConfig(observer.ICEPathMTUConfig(pair.Local, pair.Remote))
	a.pathMTU = &pathMTUState{pair: pair, config: config, current: config.BaseUDP, upper: config.MaxUDP, target: config.BaseUDP, next: now}
	a.publishPathMTU("path-reset")
}

// tickPathMTU is run by the existing connectivity task, at most once per second
// during normal connected operation. No additional polling goroutine is needed.
func (a *Agent) tickPathMTU(now time.Time) {
	observer, ok := a.net.(PathMTUObserver)
	pair := a.getSelectedPair()
	if !ok || pair == nil || a.connectionState != ConnectionStateConnected {
		return
	}
	config := normalizePathMTUConfig(observer.ICEPathMTUConfig(pair.Local, pair.Remote))
	if a.pathMTU == nil || a.pathMTU.pair != pair || a.pathMTU.config != config {
		a.resetPathMTU(pair, now)
	}
	p := a.pathMTU
	if !config.Probe || !pair.Local.NetworkType().IsUDP() || !pair.Remote.NetworkType().IsUDP() {
		return
	}
	if p.pending {
		if now.Sub(p.sent) < 2*time.Second {
			return
		}
		p.pending = false
		a.pathMTUFailure(now)
	}
	if now.Before(p.next) {
		return
	}
	msg, err := a.makePathMTUProbe(p.target)
	if err != nil {
		p.next = now.Add(30 * time.Second)
		return
	}
	p.transaction = msg.TransactionID
	p.sent = now
	p.pending = true
	// writeTo can suppress a socket error in upstream ICE; a short write must
	// never count as a sent probe. Even a full write still requires a verified ACK.
	n, err := pair.Local.writeTo(msg.Raw, pair.Remote)
	if err != nil || n != len(msg.Raw) {
		p.pending = false
		a.pathMTUFailure(now)
	}
}

func (a *Agent) makePathMTUProbe(size int) (*stun.Message, error) {
	setters := []stun.Setter{stun.BindingRequest, stun.TransactionID,
		stun.NewUsername(a.remoteUfrag + ":" + a.localUfrag), PriorityAttr(a.getSelectedPair().Local.Priority()),
		stun.NewSoftware("NanoKVM OS PMTU")}
	if a.isControlling.Load() {
		setters = append(setters, AttrControlling(a.tieBreaker))
	} else {
		setters = append(setters, AttrControlled(a.tieBreaker))
	}
	msg, err := stun.Build(setters...)
	if err != nil {
		return nil, err
	}
	remaining := size - len(msg.Raw) - 24 - 8 // MESSAGE-INTEGRITY and FINGERPRINT.
	if remaining < 0 || remaining%4 != 0 {
		return nil, fmt.Errorf("invalid probe size %d", size)
	}
	// Repeated optional SOFTWARE attributes are ignored after the first by STUN
	// receivers. Each UTF-8 string stays below 128 characters (RFC 8489 14.14).
	// Unlike RFC 5780 PADDING (comprehension-required), this needs no browser
	// NAT-behaviour-discovery extension. All bytes are covered by integrity.
	for remaining > 0 {
		valueBytes := remaining - 4
		if valueBytes > 120 {
			valueBytes = 120
		}
		msg.Add(stun.AttrSoftware, bytes.Repeat([]byte{' '}, valueBytes))
		remaining -= valueBytes + 4
	}
	if err := stun.NewShortTermIntegrity(a.remotePwd).AddTo(msg); err != nil {
		return nil, err
	}
	if err := stun.Fingerprint.AddTo(msg); err != nil {
		return nil, err
	}
	if len(msg.Raw) != size {
		return nil, fmt.Errorf("probe size mismatch")
	}
	return msg, nil
}

// Called only after handleInboundResponse verifies MESSAGE-INTEGRITY and finds
// the remote candidate. Matching transaction, actual pair and address are also
// required here. Probes never enter nomination/renomination success handling.
func (a *Agent) handlePathMTUResponse(msg *stun.Message, local, remote Candidate, source netip.AddrPort, now time.Time) bool {
	p := a.pathMTU
	if p == nil || !p.pending || msg.TransactionID != p.transaction {
		return false
	}
	if p.pair != a.getSelectedPair() || local.ID() != p.pair.Local.ID() || remote.ID() != p.pair.Remote.ID() || source != p.pair.Remote.addrPort() || now.Sub(p.sent) > 2*time.Second {
		return true
	}
	p.pending = false
	p.failures = 0
	p.successes++
	// Two independent verified responses are needed before an increase.
	if p.target > p.current && p.successes < 2 {
		p.next = now.Add(time.Second)
		return true
	}
	p.current = p.target
	p.confirmed = true
	p.successes = 0
	a.publishPathMTU("probe-confirmed")
	if p.resumeSearch > p.current {
		p.target = p.resumeSearch
		p.resumeSearch = 0
		p.next = now.Add(time.Second)
	} else if p.upper > p.current {
		p.target = p.upper
		p.next = now.Add(time.Second)
	} else {
		p.target = p.current
		p.next = now.Add(10 * time.Second)
	}
	return true
}

func (a *Agent) pathMTUFailure(now time.Time) {
	p := a.pathMTU
	p.successes = 0
	p.failures++
	p.next = now.Add(time.Second)
	if p.failures < 3 {
		return
	}
	p.failures = 0
	if p.target > p.current {
		p.upper = p.target - 4
		if p.upper > p.current {
			p.resumeSearch = ((p.current + p.upper) / 2) &^ 3
			if p.resumeSearch <= p.current {
				p.resumeSearch = p.upper
			}
			// Recheck the working size before continuing the upward search.
			// A path may have shrunk below current; repeatedly bisecting only
			// larger sizes would otherwise postpone black-hole recovery.
			p.target = p.current
		} else {
			p.target = p.current
			p.next = now.Add(10 * time.Second)
		}
		return
	}
	// Revalidation failed. Return to the known base; a failed optional probe
	// by itself cannot prove that the base is too large or that media is lost.
	p.current = p.config.BaseUDP
	p.resumeSearch = 0
	p.confirmed = false
	p.upper = p.config.MaxUDP
	p.target = p.current
	p.next = now.Add(10 * time.Second)
	a.publishPathMTU("probe-timeout")
}
