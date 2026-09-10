//go:build linux

// Package pathmtu supplies a no-fragment UDP transport and route budgets to ICE.
package pathmtu

import (
	"NanoKVM-Server/common/udpfast"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/pion/ice/v4"
	"github.com/pion/transport/v4"
	"github.com/pion/transport/v4/stdnet"
	"golang.org/x/sys/unix"
)

type socketKey struct {
	port int
	ipv6 bool
}
type Net struct {
	fast bool
	transport.Net
	mu           sync.Mutex
	dontFragment map[socketKey]bool
	callback     func(ice.PathMTUResult)
}

func NewNet(callback func(ice.PathMTUResult), fast bool) (*Net, error) {
	base, err := stdnet.NewNet()
	if err != nil {
		return nil, err
	}
	return &Net{Net: base, dontFragment: make(map[socketKey]bool), callback: callback, fast: fast}, nil
}

func (n *Net) ListenUDP(network string, addr *net.UDPAddr) (transport.UDPConn, error) {
	conn, err := n.Net.ListenUDP(network, addr)
	if err != nil {
		return nil, err
	}
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		return conn, nil
	}
	// Preserve concrete UDPConn for the mDNS/multicast implementation.
	if addr != nil && (addr.Port == 5353 || addr.IP.IsMulticast()) {
		return conn, nil
	}
	local := udp.LocalAddr().(*net.UDPAddr)
	raw, err := udp.SyscallConn()
	if err != nil {
		return conn, nil
	}
	var v4, v6 bool
	if err = raw.Control(func(fd uintptr) {
		v4 = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO) == nil
		v6 = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_MTU_DISCOVER, unix.IP_PMTUDISC_DO) == nil
	}); err != nil {
		return conn, nil
	}
	n.mu.Lock()
	n.dontFragment[socketKey{local.Port, false}] = v4
	n.dontFragment[socketKey{local.Port, true}] = v6
	n.mu.Unlock()
	if n.fast {
		return udpfast.Wrap(udp), nil
	}
	return conn, nil
}

// Pion's TURN/UDP control socket is opened through ListenPacket rather than
// ListenUDP. Keep the same no-fragment policy for its encapsulated datagrams.
func (n *Net) ListenPacket(network, address string) (net.PacketConn, error) {
	if network != "udp" && network != "udp4" && network != "udp6" {
		return n.Net.ListenPacket(network, address)
	}
	addr, err := n.Net.ResolveUDPAddr(network, address)
	if err != nil {
		return nil, err
	}
	return n.ListenUDP(network, addr)
}

func (n *Net) OnICEPathMTU(result ice.PathMTUResult) {
	if n.callback != nil {
		n.callback(result)
	}
}

func (n *Net) ICEPathMTUConfig(local, remote ice.Candidate) ice.PathMTUConfig {
	config := ice.PathMTUConfig{BaseUDP: 1232, MaxUDP: 1232, RouteID: "unknown"}
	if local == nil || remote == nil {
		return config
	}
	if !local.NetworkType().IsUDP() || !remote.NetworkType().IsUDP() {
		return config
	}
	localRelay := local.Type() == ice.CandidateTypeRelay
	relayed := localRelay || remote.Type() == ice.CandidateTypeRelay
	if relayed {
		// Keep the provisional second-leg ceiling until relay egress has been
		// qualified. A known smaller first-leg route must still reduce it.
		config.BaseUDP, config.MaxUDP, config.RouteID = 1168, 1168, "relay-unverified"
	}
	source, port := local.Address(), local.Port()
	if related := local.RelatedAddress(); related != nil && related.Address != "" && (localRelay || related.Address != "0.0.0.0") {
		source, port = related.Address, related.Port
	}
	destination := remote.Address()
	turnOverhead := 0
	if localRelay {
		relay, ok := local.(*ice.CandidateRelay)
		if !ok || relay.RelayProtocol() != "udp" {
			return config
		}
		host, _, err := net.SplitHostPort(relay.RelayTransportAddress())
		if err != nil {
			return config
		}
		destination = host
		// Largest Send Indication in current Pion TURN: STUN header 20,
		// DATA attribute 4 + up to 3 padding, IPv6 XOR-PEER-ADDRESS 24,
		// FINGERPRINT 8 = at most 59 bytes. Reserve 64, also covering the
		// smaller ChannelData header. Never budget using ChannelData alone.
		turnOverhead = 64
	}
	src, dst := net.ParseIP(source), net.ParseIP(destination)
	if src == nil || dst == nil {
		return config
	}
	ipv6 := dst.To4() == nil
	n.mu.Lock()
	df := n.dontFragment[socketKey{port, ipv6}]
	n.mu.Unlock()
	route, err := lookupRoute(src, dst)
	if err != nil {
		return config
	}
	iface, err := net.InterfaceByIndex(route.ifindex)
	if err != nil {
		return config
	}
	mtu := iface.MTU
	if route.mtu > 0 && route.mtu < mtu {
		mtu = route.mtu
	}
	if ipv6 {
		raw, _ := os.ReadFile("/proc/sys/net/ipv6/conf/" + iface.Name + "/mtu")
		if value, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && value > 0 && value < mtu {
			mtu = value
		}
	}
	// Internet probing is bounded by standard Ethernet size. Jumbo frames are
	// not needed for this encoder and require separate receiver qualification.
	if mtu > 1500 {
		mtu = 1500
	}
	overhead := 28
	if ipv6 {
		overhead = 48
	}
	routeBudget := mtu - overhead - turnOverhead
	if routeBudget <= 0 {
		routeBudget = -1
	} // Explicit no-room, not the zero/default convention.
	if !relayed || routeBudget < config.MaxUDP {
		config.MaxUDP = routeBudget
	}
	if config.BaseUDP > config.MaxUDP {
		config.BaseUDP = config.MaxUDP
	}
	config.Probe = df && !relayed
	config.RouteID = fmt.Sprintf("%d/%d/%x", route.ifindex, mtu, route.gateway)
	if relayed {
		config.RouteID = fmt.Sprintf("relay/%s/%s/%d", config.RouteID, destination, turnOverhead)
	}
	return config
}

type routeInfo struct {
	ifindex, mtu int
	gateway      []byte
}

func netlinkAttr(kind uint16, value []byte) []byte {
	b := make([]byte, (len(value)+4+3)&^3)
	binary.NativeEndian.PutUint16(b, uint16(len(value)+4))
	binary.NativeEndian.PutUint16(b[2:], kind)
	copy(b[4:], value)
	return b
}

func lookupRoute(source, destination net.IP) (routeInfo, error) {
	family, bits := byte(unix.AF_INET6), byte(128)
	dst, src := destination.To16(), source.To16()
	if v4 := destination.To4(); v4 != nil {
		family, bits = unix.AF_INET, 32
		dst = v4
		src = source.To4()
	}
	if src == nil || dst == nil {
		return routeInfo{}, fmt.Errorf("route address family mismatch")
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return routeInfo{}, err
	}
	defer unix.Close(fd)
	timeout := unix.NsecToTimeval(100000000)
	if err = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		return routeInfo{}, err
	}
	msg := make([]byte, 28)
	binary.NativeEndian.PutUint16(msg[4:], unix.RTM_GETROUTE)
	binary.NativeEndian.PutUint16(msg[6:], unix.NLM_F_REQUEST)
	binary.NativeEndian.PutUint32(msg[8:], 1)
	msg[16], msg[17], msg[18] = family, bits, bits
	msg = append(msg, netlinkAttr(unix.RTA_DST, dst)...)
	msg = append(msg, netlinkAttr(unix.RTA_SRC, src)...)
	binary.NativeEndian.PutUint32(msg, uint32(len(msg)))
	if err = unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return routeInfo{}, err
	}
	data := make([]byte, 8192)
	size, _, err := unix.Recvfrom(fd, data, 0)
	if err != nil {
		return routeInfo{}, err
	}
	messages, err := syscall.ParseNetlinkMessage(data[:size])
	if err != nil {
		return routeInfo{}, err
	}
	for _, message := range messages {
		if message.Header.Seq != 1 {
			continue
		}
		if message.Header.Type == unix.NLMSG_ERROR {
			return routeInfo{}, fmt.Errorf("route lookup rejected")
		}
		if message.Header.Type != unix.RTM_NEWROUTE {
			continue
		}
		attributes, err := syscall.ParseNetlinkRouteAttr(&message)
		if err != nil {
			return routeInfo{}, err
		}
		result := routeInfo{}
		for _, attribute := range attributes {
			switch attribute.Attr.Type {
			case unix.RTA_OIF:
				if len(attribute.Value) >= 4 {
					result.ifindex = int(binary.NativeEndian.Uint32(attribute.Value))
				}
			case unix.RTA_GATEWAY:
				result.gateway = append([]byte(nil), attribute.Value...)
			case unix.RTA_METRICS:
				nested := attribute.Value
				for len(nested) >= 4 {
					size := int(binary.NativeEndian.Uint16(nested))
					kind := binary.NativeEndian.Uint16(nested[2:])
					if size < 4 || size > len(nested) {
						break
					}
					if kind == unix.RTAX_MTU && size >= 8 {
						result.mtu = int(binary.NativeEndian.Uint32(nested[4:]))
					}
					aligned := (size + 3) &^ 3
					if aligned > len(nested) {
						break
					}
					nested = nested[aligned:]
				}
			}
		}
		if result.ifindex > 0 {
			return result, nil
		}
	}
	return routeInfo{}, fmt.Errorf("no route interface returned")
}

var _ ice.PathMTUObserver = (*Net)(nil)
var _ transport.Net = (*Net)(nil)
