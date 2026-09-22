// Package webrtcdtls defines the DTLS policy shared by every NanoKVM WebRTC peer.
package webrtcdtls

import (
	"github.com/pion/dtls/v3"
	"github.com/pion/webrtc/v4"
)

// CipherSuites returns the global DTLS 1.2 preference order. ECDSA and RSA
// variants preserve compatibility with either certificate type; Pion filters
// the incompatible variants after the connection certificate is selected.
func CipherSuites() []dtls.CipherSuiteID {
	return []dtls.CipherSuiteID{
		dtls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		dtls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		dtls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	}
}

// Configure makes NanoKVM the DTLS server for answers and enables ChaCha20-
// Poly1305 preference with AES-128-GCM fallback. SRTP policy is configured
// separately by the caller and is intentionally untouched here.
func Configure(settings *webrtc.SettingEngine) error {
	settings.SetDTLSCipherSuites(CipherSuites()...)
	return settings.SetAnsweringDTLSRole(webrtc.DTLSRoleServer)
}
