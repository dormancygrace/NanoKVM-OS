// Package icelocal keeps a browser's WebRTC media on the device address its
// signaling connection reached.
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
