package vm

import (
	"errors"
	"os"
	"strings"
	"sync"

	"NanoKVM-Server/internal/atomicfile"
)

// The video transport of the device: the player a browser starts with when
// it has no choice of its own. Administrators set it with the other video
// settings; each browser may still choose its own player, and viewers of
// different transports watch at the same time.
const (
	TransportDirect = "direct"
	TransportWebRTC = "webrtc"
	TransportMJPEG  = "mjpeg"
)

var (
	transportFile = "/kvmapp/kvm/transport"
	transportMu   sync.Mutex
)

func validTransport(value string) bool {
	return value == TransportDirect || value == TransportWebRTC || value == TransportMJPEG
}

// StreamTransport returns the device transport; Direct until one is set.
func StreamTransport() string {
	transportMu.Lock()
	defer transportMu.Unlock()
	return readTransportLocked()
}

func readTransportLocked() string {
	data, err := os.ReadFile(transportFile)
	if value := strings.TrimSpace(string(data)); err == nil && validTransport(value) {
		return value
	}
	return TransportDirect
}

// SetStreamTransport stores the device transport.
func SetStreamTransport(value string) error {
	if !validTransport(value) {
		return errors.New("transport must be direct, webrtc or mjpeg")
	}
	transportMu.Lock()
	defer transportMu.Unlock()
	if _, err := os.Stat(transportFile); err == nil && readTransportLocked() == value {
		return nil
	}
	return atomicfile.Write(transportFile, []byte(value+"\n"), 0o644)
}
