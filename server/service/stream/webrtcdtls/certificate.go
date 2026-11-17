package webrtcdtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// Without a certificate in its configuration, every PeerConnection generates
// one: a P-256 key and a self-signed certificate, about 7 ms of the single
// core while the browser waits for the answer. Browsers check a WebRTC
// certificate only against the fingerprint in the SDP, so one serves every
// peer. Pion's certificates last a month (and start a day early); a new one
// is made a day before the old one expires, or if the clock went back before
// its creation time.
var (
	certMu      sync.Mutex
	cert        webrtc.Certificate
	haveCert    bool
	certCreated time.Time
)

// Certificates returns the DTLS certificate list for a PeerConnection
// configuration: the one certificate shared by every NanoKVM peer.
func Certificates() ([]webrtc.Certificate, error) {
	certMu.Lock()
	defer certMu.Unlock()
	now := time.Now()
	if !haveCert || now.Add(24*time.Hour).After(cert.Expires()) || now.Before(certCreated) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		fresh, err := webrtc.GenerateCertificate(key)
		if err != nil {
			return nil, err
		}
		cert, haveCert = *fresh, true
		// Keep wall time: the monotonic clock cannot detect a clock correction.
		certCreated = now.Round(0)
	}
	return []webrtc.Certificate{cert}, nil
}
