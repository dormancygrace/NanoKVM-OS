package vm

import (
	"errors"
	"os"
	"strings"
	"sync"

	"NanoKVM-Server/internal/atomicfile"
)

// The video transport of the device: the player a browser starts with when
// it has no choice of its own, and the transport RustDesk follows (it accepts
// WebRTC connections exactly when this is WebRTC). Administrators set it with
// the other video settings; each browser may still choose its own player.
const (
	TransportDirect = "direct"
	TransportWebRTC = "webrtc"
	TransportMJPEG  = "mjpeg"
)

var (
	transportFile      = "/kvmapp/kvm/transport"
	transportMu        sync.Mutex
	transportListeners []func(string)
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

// StreamTransportSet reports whether the device transport was ever stored.
func StreamTransportSet() bool {
	transportMu.Lock()
	defer transportMu.Unlock()
	_, err := os.Stat(transportFile)
	return err == nil
}

// OnStreamTransportChange registers fn, called with the new transport after
// each change.
func OnStreamTransportChange(fn func(string)) {
	transportMu.Lock()
	defer transportMu.Unlock()
	transportListeners = append(transportListeners, fn)
}

// SetStreamTransport stores the device transport and tells the listeners when
// it changed.
func SetStreamTransport(value string) error {
	if !validTransport(value) {
		return errors.New("transport must be direct, webrtc or mjpeg")
	}
	transportMu.Lock()
	_, statErr := os.Stat(transportFile)
	previous := readTransportLocked()
	if statErr == nil && previous == value {
		transportMu.Unlock()
		return nil
	}
	if err := atomicfile.Write(transportFile, []byte(value+"\n"), 0o644); err != nil {
		transportMu.Unlock()
		return err
	}
	listeners := append([]func(string){}, transportListeners...)
	transportMu.Unlock()
	if previous != value {
		for _, fn := range listeners {
			fn(value)
		}
	}
	return nil
}
