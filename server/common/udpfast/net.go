// Package udpfast preserves Go's socket polling while avoiding a scheduler
// transition for immediately completing, nonblocking UDP sends on Linux.
package udpfast

import (
	"github.com/pion/transport/v4"
	"github.com/pion/transport/v4/stdnet"
	"net"
)

type Net struct{ transport.Net }

func NewNet() (*Net, error) {
	base, err := stdnet.NewNet()
	if err != nil {
		return nil, err
	}
	return &Net{Net: base}, nil
}

func (n *Net) ListenUDP(network string, addr *net.UDPAddr) (transport.UDPConn, error) {
	conn, err := n.Net.ListenUDP(network, addr)
	if err != nil {
		return nil, err
	}
	// x/net/ipv4 and ipv6 require a concrete *net.UDPConn for mDNS reads.
	// Preserve that type for multicast and the standard mDNS listener port.
	if addr != nil && (addr.IP.IsMulticast() || addr.Port == 5353) {
		return conn, nil
	}
	if udp, ok := conn.(*net.UDPConn); ok {
		return Wrap(udp), nil
	}
	return conn, nil
}

var _ transport.Net = (*Net)(nil)
