package webrtc

import (
	"os"

	"github.com/pion/dtls/v3"
)

const srtpProfilePreferenceEnv = "NANOKVM_WEBRTC_SRTP_PROFILE"

func srtpProtectionProfiles() []dtls.SRTPProtectionProfile {
	if os.Getenv(srtpProfilePreferenceEnv) == "gcm" {
		return []dtls.SRTPProtectionProfile{
			dtls.SRTP_AEAD_AES_128_GCM,
			dtls.SRTP_AES128_CM_HMAC_SHA1_80,
		}
	}

	return []dtls.SRTPProtectionProfile{
		dtls.SRTP_AES128_CM_HMAC_SHA1_80,
		dtls.SRTP_AEAD_AES_128_GCM,
	}
}
