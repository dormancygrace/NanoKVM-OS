// Package icelocal keeps a browser's WebRTC media on the device address its
// signaling connection reached, and leaves out STUN and TURN for a browser on
// that address's subnet.
//
// The device offers ICE host candidates on every address it has (Ethernet,
// Wi-Fi, VPN), and the browser may pick any pair that answers: a page opened
// over Ethernet could get its video over Wi-Fi. Filter limits the device's
// candidates to the address the page's WebSocket connected to, so the media
// takes the same interface and path as the page.
package icelocal

import (
	"net"
	"net/http"

	"github.com/pion/webrtc/v4"
)

// Filter returns the ICE address filter for a peer signaled over r: only the
// device address r arrived at. It returns nil (every address) when that
// address is a loopback one, as for a request relayed by a local proxy, whose
// browser is elsewhere.
func Filter(r *http.Request) func(net.IP) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok || local.IP == nil || local.IP.IsLoopback() || local.IP.IsUnspecified() {
		return nil
	}
	want := local.IP
	return func(ip net.IP) bool { return ip.Equal(want) }
}

// Servers returns the STUN and TURN servers for a peer signaled over r. A
// browser on the subnet of the device address r arrived at reaches that
// address directly, so it gets none: no server-reflexive or relay candidates
// to gather, resolve and check on either side. Any other browser, or a request
// relayed by a local proxy, gets servers unchanged.
func Servers(r *http.Request, servers []webrtc.ICEServer) []webrtc.ICEServer {
	if len(servers) == 0 || !onLink(r) {
		return servers
	}
	return []webrtc.ICEServer{} // a list, also in JSON: the audio page passes it on as is
}

// onLink reports whether the browser of r is on the subnet of the device
// address r arrived at.
func onLink(r *http.Request) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok || local.IP == nil || local.IP.IsLoopback() || local.IP.IsUnspecified() {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	remote := net.ParseIP(host)
	if remote == nil {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if prefix, ok := addr.(*net.IPNet); ok && prefix.IP.Equal(local.IP) {
			return prefix.Contains(remote)
		}
	}
	return false
}
