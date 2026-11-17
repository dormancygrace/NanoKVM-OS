// Package icelocal keeps a browser's WebRTC media on the device interface its
// signaling connection reached.
//
// The device offers ICE host candidates on every address it has (Ethernet,
// Wi-Fi, VPN), and the browser may pick any pair that answers: a page opened
// over Ethernet could get its video over Wi-Fi. A Path limits the device's
// candidates to the addresses of the interface the page's WebSocket connected
// to, and binds every ICE, STUN and TURN socket to that interface, so that
// the media also leaves through it whatever the routing table prefers.
package icelocal

import (
	"context"
	"net"
	"net/http"
	"syscall"

	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
	"golang.org/x/sys/unix"
)

// Path is the device end of a browser's media.
type Path struct {
	// Addresses keeps the candidate addresses; nil keeps every address.
	Addresses func(net.IP) bool
	// Interface is the interface every socket is bound to; nil binds none.
	Interface *net.Interface
}

// For returns the path for a peer signaled over r: the device interface r
// arrived at, with its addresses of both families, so that a page opened over
// IPv6 can still use an IPv4-only TURN server on that interface. A request
// that arrived at a loopback address, as one relayed by a local proxy, comes
// from a browser elsewhere and gets every address and no binding. If the
// address is no longer on any interface, only that address is kept.
func For(r *http.Request) Path {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok || local.IP == nil || local.IP.IsLoopback() || local.IP.IsUnspecified() {
		return Path{}
	}
	path := Path{Addresses: keep([]net.IP{local.IP})}
	if ifaces, err := net.Interfaces(); err == nil {
		for i := range ifaces {
			if ips := addresses(ifaces[i]); contains(ips, local.IP) {
				path = Path{Addresses: keep(ips), Interface: &ifaces[i]}
				break
			}
		}
	}
	return path
}

// Net returns the socket layer for ICE on this path.
func (p Path) Net() (transport.Net, error) {
	base, err := stdnet.NewNet()
	if err != nil || p.Interface == nil {
		return base, err
	}
	return &boundNet{Net: base, device: p.Interface.Name}, nil
}

// Index is the interface index of the path, 0 for none.
func (p Path) Index() int {
	if p.Interface == nil {
		return 0
	}
	return p.Interface.Index
}

// boundNet binds each socket to one interface (SO_BINDTODEVICE). A source
// address alone does not choose the egress interface: Linux routes a packet
// from 192.0.2.10 through whichever interface has the preferred route.
type boundNet struct {
	transport.Net
	device string
}

func (n *boundNet) control(_, _ string, c syscall.RawConn) error {
	var err error
	if controlErr := c.Control(func(fd uintptr) { err = unix.BindToDevice(int(fd), n.device) }); controlErr != nil {
		return controlErr
	}
	return err
}

func (n *boundNet) ListenUDP(network string, addr *net.UDPAddr) (transport.UDPConn, error) {
	// mDNS listens on every interface for multicast; leave it unbound.
	if addr != nil && (addr.Port == 5353 || addr.IP.IsMulticast()) {
		return n.Net.ListenUDP(network, addr)
	}
	address := ""
	if addr != nil {
		address = addr.String()
	}
	config := net.ListenConfig{Control: n.control}
	conn, err := config.ListenPacket(context.Background(), network, address)
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}

// Pion's TURN/UDP control socket is opened through ListenPacket.
func (n *boundNet) ListenPacket(network, address string) (net.PacketConn, error) {
	if network != "udp" && network != "udp4" && network != "udp6" {
		return n.Net.ListenPacket(network, address)
	}
	addr, err := n.Net.ResolveUDPAddr(network, address)
	if err != nil {
		return nil, err
	}
	return n.ListenUDP(network, addr)
}

func (n *boundNet) dialer(local net.Addr) *net.Dialer {
	return &net.Dialer{LocalAddr: local, Control: n.control}
}

func (n *boundNet) Dial(network, address string) (net.Conn, error) {
	return n.dialer(nil).Dial(network, address)
}

func (n *boundNet) DialUDP(network string, laddr, raddr *net.UDPAddr) (transport.UDPConn, error) {
	var local net.Addr
	if laddr != nil {
		local = laddr
	}
	conn, err := n.dialer(local).Dial(network, raddr.String())
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}

func (n *boundNet) DialTCP(network string, laddr, raddr *net.TCPAddr) (transport.TCPConn, error) {
	var local net.Addr
	if laddr != nil {
		local = laddr
	}
	conn, err := n.dialer(local).Dial(network, raddr.String())
	if err != nil {
		return nil, err
	}
	return conn.(*net.TCPConn), nil
}

func (n *boundNet) CreateDialer(d *net.Dialer) transport.Dialer {
	bound := *d
	bound.Control = n.control
	return n.Net.CreateDialer(&bound)
}

func (n *boundNet) CreateListenConfig(c *net.ListenConfig) transport.ListenConfig {
	bound := *c
	bound.Control = n.control
	return n.Net.CreateListenConfig(&bound)
}

func keep(ips []net.IP) func(net.IP) bool {
	return func(ip net.IP) bool { return contains(ips, ip) }
}

func addresses(iface net.Interface) []net.IP {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if prefix, ok := addr.(*net.IPNet); ok {
			ips = append(ips, prefix.IP)
		}
	}
	return ips
}

func contains(ips []net.IP, ip net.IP) bool {
	for _, x := range ips {
		if x.Equal(ip) {
			return true
		}
	}
	return false
}
