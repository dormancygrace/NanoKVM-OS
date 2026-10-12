// Package srtpaccel is the SRTP policy shared by every NanoKVM WebRTC peer:
// the protection profile order and the C906 accelerators for
// AES128_CM_HMAC_SHA1_80 (CryptoDMA AES-CTR, multi-buffer HMAC-SHA1).
package srtpaccel

import "github.com/pion/webrtc/v4"

// Configure selects the accelerators once per process and sets the SRTP
// profile order on settings. Every peer connection must call it: Pion's own
// default puts AEAD_AES_128_GCM first, which has neither hardware nor
// assembly here and would run Go's generic AES and GHASH.
func Configure(settings *webrtc.SettingEngine) {
	initializeHardwareAES()
	initializeHMACBatch()
	settings.SetSRTPProtectionProfiles(srtpProtectionProfiles()...)
}
