package srtpaccel

import "github.com/pion/dtls/v3"

func srtpProtectionProfiles() []dtls.SRTPProtectionProfile {
	return []dtls.SRTPProtectionProfile{
		dtls.SRTP_AES128_CM_HMAC_SHA1_80,
		dtls.SRTP_AEAD_AES_128_GCM,
	}
}
