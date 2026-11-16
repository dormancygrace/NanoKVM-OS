package utils

import (
	"log"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Connection limits shared by the web server's listeners (HTTPS, HTTP and
// the HTTP-to-HTTPS redirect), so a flood of connections cannot exhaust the
// single core, its memory or its file descriptors.
//
// A WebSocket or a stream counts for its whole life. One desktop tab holds
// one HTTP/2 connection (fetches and the LED EventSource share it) and a
// separate HTTP/1.1 connection per WebSocket: input and control, video
// (H.264 signalling, MJPEG tiles or the direct stream), USB audio, at times
// a stall probe, and the picoclaw gateway, remote image or terminal while
// they are open: about 7. Over plain HTTP a browser adds up to 6 pooled
// connections per host. 64 per source therefore leaves room for about eight
// desktop tabs from one address, or a few people behind one NAT; 256 in
// total serves four such sources. WebRTC media is UDP and does not count.
// An IPv6 /64 is usually a whole network segment (link-local peers all fall
// in fe80::/64), so its 64 are shared by the machines on it, like a NAT.
const (
	maxConnections          = 256
	maxConnectionsPerSource = 64
	refusalLogInterval      = 10 * time.Second
)

var connections = newConnLimiter(maxConnections, maxConnectionsPerSource)

// LimitConnections closes, as soon as they are accepted and before any TLS
// work, the connections over the shared limits. Loopback peers (add-ons and
// the server's own calls) are neither counted nor limited, so a remote flood
// cannot cut them off. Wrap it around RawListener, not under it: RawListener
// needs the *net.TCPConn, and the counted connection keeps it reachable
// through TCP().
func LimitConnections(l net.Listener) net.Listener { return limitListener{l, connections} }

type limitListener struct {
	net.Listener
	limits *connLimiter
}

// Accept never reports a refusal as an error: Serve would stop on it.
func (l limitListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		source, limited := connSource(conn.RemoteAddr())
		if !limited {
			return conn, nil
		}
		if l.limits.acquire(source) {
			release := func() { l.limits.release(source) }
			if tcp, ok := conn.(*net.TCPConn); ok {
				return &countedTCPConn{TCPConn: tcp, release: release}, nil
			}
			return &countedConn{Conn: conn, release: release}, nil
		}
		_ = conn.Close()
		l.limits.logRefusal(conn.RemoteAddr())
	}
}

// connSource returns the accounting key of a peer: an IPv4 address or an
// IPv6 /64, the smallest block one subscriber usually holds. Loopback peers
// and unparsable addresses are not limited.
func connSource(addr net.Addr) (netip.Addr, bool) {
	var ip netip.Addr
	switch a := addr.(type) {
	case *net.TCPAddr:
		ip = a.AddrPort().Addr()
	default:
		ap, err := netip.ParseAddrPort(addr.String())
		if err != nil {
			return netip.Addr{}, false
		}
		ip = ap.Addr()
	}
	ip = ip.Unmap().WithZone("")
	if !ip.IsValid() || ip.IsLoopback() {
		return netip.Addr{}, false
	}
	if ip.Is6() {
		ip = netip.PrefixFrom(ip, 64).Masked().Addr()
	}
	return ip, true
}

type connLimiter struct {
	mu        sync.Mutex
	total     int
	maxTotal  int
	maxSource int
	perSource map[netip.Addr]int
	lastLog   time.Time
	refused   int
}

func newConnLimiter(maxTotal, maxSource int) *connLimiter {
	return &connLimiter{maxTotal: maxTotal, maxSource: maxSource, perSource: map[netip.Addr]int{}}
}

func (l *connLimiter) acquire(source netip.Addr) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal || l.perSource[source] >= l.maxSource {
		return false
	}
	l.total++
	l.perSource[source]++
	return true
}

func (l *connLimiter) release(source netip.Addr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	if l.perSource[source]--; l.perSource[source] <= 0 {
		delete(l.perSource, source)
	}
}

// logRefusal reports refusals at most once per refusalLogInterval, so the
// flood does not become a log flood.
func (l *connLimiter) logRefusal(peer net.Addr) {
	l.mu.Lock()
	l.refused++
	now := time.Now()
	if now.Sub(l.lastLog) < refusalLogInterval {
		l.mu.Unlock()
		return
	}
	refused, total := l.refused, l.total
	l.refused, l.lastLog = 0, now
	l.mu.Unlock()
	log.Printf("connection limit: refused %d connection(s), latest from %s (%d open)", refused, peer, total)
}

// countedTCPConn is a counted plain TCP connection (the plain-HTTP
// listeners): handlers and hijacked WebSockets keep every *net.TCPConn
// method, writev and sendfile included. It releases its slot once, on Close.
type countedTCPConn struct {
	*net.TCPConn
	once    sync.Once
	release func()
}

func (c *countedTCPConn) Close() error {
	err := c.TCPConn.Close()
	c.once.Do(c.release)
	return err
}

// TCP returns the accepted TCP connection, for socket queries (IngressInterface).
func (c *countedTCPConn) TCP() *net.TCPConn { return c.TCPConn }

// countedConn is a counted connection of another type, such as RawListener's
// (under TLS, where only crypto/tls and IngressInterface see it). It
// releases its slot once, when it is closed, including after net/http hands
// it to a WebSocket handler.
type countedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *countedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// TCP returns the accepted TCP connection, for socket queries (IngressInterface).
func (c *countedConn) TCP() *net.TCPConn {
	switch conn := c.Conn.(type) {
	case *net.TCPConn:
		return conn
	case interface{ TCP() *net.TCPConn }:
		return conn.TCP()
	}
	return nil
}
