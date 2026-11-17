// Package icelocal keeps a browser's WebRTC media on the device interface its
// signaling connection reached.
//
// The device offers ICE host candidates on every address it has (Ethernet,
// Wi-Fi, VPN), and the browser may pick any pair that answers: a page opened
// over Ethernet could get its video over Wi-Fi. Filter limits the device's
// candidates, and the local ends of its STUN and TURN connections, to the
// interface the page's WebSocket connected to, so the media takes the same
// interface as the page.
package icelocal

import (
	"net"
	"net/http"
)

// Filter returns the ICE address filter for a peer signaled over r: the
// addresses of the device interface r arrived at, of both families, so that a
// page opened over IPv6 can still use an IPv4-only TURN server on that
// interface. It returns nil (every address) when r arrived at a loopback
// address, as for a request relayed by a local proxy, whose browser is
// elsewhere. If the address is no longer on any interface, only it is kept.
func Filter(r *http.Request) func(net.IP) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok || local.IP == nil || local.IP.IsLoopback() || local.IP.IsUnspecified() {
		return nil
	}
	want := []net.IP{local.IP}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if ips := addresses(iface); contains(ips, local.IP) {
				want = ips
				break
			}
		}
	}
	return func(ip net.IP) bool { return contains(want, ip) }
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
