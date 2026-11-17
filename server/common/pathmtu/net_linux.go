//go:build linux

// Package pathmtu supplies a no-fragment UDP transport and route budgets to ICE.
package pathmtu

import (
	"NanoKVM-Server/common/udpbatch"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/pion/ice/v4"
	"github.com/pion/transport/v5"
	"golang.org/x/sys/unix"
)

type socketKey struct {
	port int
	ipv6 bool
}
type Net struct {
	transport.Net
	mu           sync.Mutex
	dontFragment map[socketKey]bool
	callback     func(ice.PathMTUResult)
	oif          int
	batch        bool
	batchConns   map[*udpbatch.Conn]struct{}
}

// NewNet wraps base. When base binds its sockets to one interface, oif is
// that interface's index, so that route budgets come from the routes through
// it; 0 means any interface.
func NewNet(base transport.Net, oif int, callback func(ice.PathMTUResult)) *Net {
	return &Net{Net: base, dontFragment: make(map[socketKey]bool), callback: callback, oif: oif, batchConns: make(map[*udpbatch.Conn]struct{})}
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
	if n.batchEnabled() {
		var wrapped *udpbatch.Conn
		wrapped, ok = udpbatch.Wrap(udp, func() { n.removeBatchConn(wrapped) })
		if ok && n.addBatchConn(wrapped) {
			return wrapped, nil
		}
	}
	return conn, nil
}

const maxBatchConns = 16

// EnableFrameBatching opts this Net into the bounded frame-scoped adapter.
// It must be called before ICE starts opening sockets; sockets opened after the
// limit are deliberately left on the ordinary synchronous implementation.
func (n *Net) EnableFrameBatching() {
	n.mu.Lock()
	n.batch = true
	n.mu.Unlock()
}

func (n *Net) batchEnabled() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.batch
}

func (n *Net) addBatchConn(c *udpbatch.Conn) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.batch || len(n.batchConns) >= maxBatchConns {
		return false
	}
	if n.batchConns == nil {
		n.batchConns = make(map[*udpbatch.Conn]struct{})
	}
	n.batchConns[c] = struct{}{}
	return true
}

func (n *Net) removeBatchConn(c *udpbatch.Conn) {
	n.mu.Lock()
	delete(n.batchConns, c)
	n.mu.Unlock()
}

// BeginFrame starts buffering media writes on every wrapped socket.
func (n *Net) BeginFrame() {
	var conns [maxBatchConns]*udpbatch.Conn
	n.mu.Lock()
	count := 0
	for c := range n.batchConns {
		conns[count] = c
		count++
	}
	n.mu.Unlock()
	for i := 0; i < count; i++ {
		conns[i].BeginFrame()
	}
}

// EndFrame flushes each wrapped socket and returns the first error while still
// flushing all other active sockets.
func (n *Net) EndFrame() error {
	var conns [maxBatchConns]*udpbatch.Conn
	n.mu.Lock()
	count := 0
	for c := range n.batchConns {
		conns[count] = c
		count++
	}
	n.mu.Unlock()
	var first error
	for i := 0; i < count; i++ {
		if err := conns[i].EndFrame(); err != nil && first == nil {
			first = err
		}
	}
	return first
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
	route, err := lookupRoute(src, dst, n.oif)
	if n.oif > 0 {
		route, err = boundRoute(route, src, dst, n.oif)
	}
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

func lookupRoute(source, destination net.IP, oif int) (routeInfo, error) {
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
	if oif > 0 {
		index := make([]byte, 4)
		binary.NativeEndian.PutUint32(index, uint32(oif))
		msg = append(msg, netlinkAttr(unix.RTA_OIF, index)...)
	}
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

// boundRoute is the route that a socket bound to interface oif takes from
// source to destination. A netlink route query is not that lookup: for IPv6
// with a source address the kernel takes RTA_OIF as a preference and may answer
// with another interface's route. A UDP socket bound to the interface and to
// source and connected to destination (connect sends nothing) makes the kernel
// do the bound lookup, with the same source-policy rules as the media sockets;
// its IP_MTU or IPV6_MTU is that route's MTU. The netlink answer lends its
// gateway only when it names the same interface.
func boundRoute(queried routeInfo, source, destination net.IP, oif int) (routeInfo, error) {
	iface, err := net.InterfaceByIndex(oif)
	if err != nil {
		return routeInfo{}, err
	}
	family, level, option := unix.AF_INET6, unix.IPPROTO_IPV6, unix.IPV6_MTU
	var local, remote unix.Sockaddr
	if src4, dst4 := source.To4(), destination.To4(); dst4 != nil {
		if src4 == nil {
			return routeInfo{}, fmt.Errorf("route address family mismatch")
		}
		family, level, option = unix.AF_INET, unix.IPPROTO_IP, unix.IP_MTU
		local, remote = &unix.SockaddrInet4{Addr: [4]byte(src4)}, &unix.SockaddrInet4{Port: 9, Addr: [4]byte(dst4)}
	} else {
		src16, dst16 := source.To16(), destination.To16()
		if src16 == nil || source.To4() != nil {
			return routeInfo{}, fmt.Errorf("route address family mismatch")
		}
		zone := func(ip net.IP) uint32 {
			if ip.IsLinkLocalUnicast() {
				return uint32(oif)
			}
			return 0
		}
		local = &unix.SockaddrInet6{Addr: [16]byte(src16), ZoneId: zone(source)}
		remote = &unix.SockaddrInet6{Port: 9, Addr: [16]byte(dst16), ZoneId: zone(destination)}
	}
	fd, err := unix.Socket(family, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return routeInfo{}, err
	}
	defer unix.Close(fd)
	if err = unix.BindToDevice(fd, iface.Name); err != nil {
		return routeInfo{}, err
	}
	if err = unix.Bind(fd, local); err != nil {
		return routeInfo{}, err
	}
	if err = unix.Connect(fd, remote); err != nil {
		return routeInfo{}, err
	}
	mtu, err := unix.GetsockoptInt(fd, level, option)
	if err != nil {
		return routeInfo{}, err
	}
	route := routeInfo{ifindex: oif, mtu: mtu}
	if queried.ifindex == oif {
		route.gateway = queried.gateway
	}
	return route, nil
}

var _ ice.PathMTUObserver = (*Net)(nil)
var _ transport.Net = (*Net)(nil)
