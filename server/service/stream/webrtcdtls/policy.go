// Package webrtcdtls defines the DTLS policy shared by every NanoKVM WebRTC peer.
package webrtcdtls

import (
	"sync"

	"github.com/pion/dtls/v3"
	"github.com/pion/webrtc/v4"
)

// Cipher preferences: the suite family NanoKVM offers first. NanoKVM answers
// as the DTLS server and picks the first of its own suites the client
// offers, so the preference decides whenever the client supports both.
const (
	PreferChaCha20 = "chacha20"
	PreferAESGCM   = "aes-gcm"
)

var (
	preferenceMu sync.RWMutex
	preference   = func() string { return PreferChaCha20 }
)

// SetPreference makes the order follow pref, read for each new connection.
func SetPreference(pref func() string) {
	preferenceMu.Lock()
	defer preferenceMu.Unlock()
	preference = pref
}

// CipherSuites returns the DTLS 1.2 preference order: ChaCha20-Poly1305
// first by default, AES-128-GCM first when preferred, the other as the
// fallback. ECDSA and RSA variants preserve compatibility with either
// certificate type; Pion filters the incompatible variants after the
// connection certificate is selected.
func CipherSuites() []dtls.CipherSuiteID {
	chacha := []dtls.CipherSuiteID{
		dtls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		dtls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	}
	aes := []dtls.CipherSuiteID{
		dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		dtls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	}
	preferenceMu.RLock()
	pref := preference()
	preferenceMu.RUnlock()
	if pref == PreferAESGCM {
		return append(aes, chacha...)
	}
	return append(chacha, aes...)
}

// Configure makes NanoKVM the DTLS server for answers and applies the
// cipher order of CipherSuites. SRTP policy is configured
// separately by the caller and is intentionally untouched here.
func Configure(settings *webrtc.SettingEngine) error {
	settings.SetDTLSCipherSuites(CipherSuites()...)
	return settings.SetAnsweringDTLSRole(webrtc.DTLSRoleServer)
}
