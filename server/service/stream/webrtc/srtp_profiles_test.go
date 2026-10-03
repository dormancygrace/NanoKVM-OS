package webrtc

import (
	"testing"

	"github.com/pion/dtls/v3"
)

func TestSRTPProtectionProfiles(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []dtls.SRTPProtectionProfile
	}{
		{
			name:  "GCM preference",
			value: "gcm",
			want: []dtls.SRTPProtectionProfile{
				dtls.SRTP_AEAD_AES_128_GCM,
				dtls.SRTP_AES128_CM_HMAC_SHA1_80,
			},
		},
		{
			name:  "CM preference",
			value: "cm",
			want: []dtls.SRTPProtectionProfile{
				dtls.SRTP_AES128_CM_HMAC_SHA1_80,
				dtls.SRTP_AEAD_AES_128_GCM,
			},
		},
		{
			name:  "empty preference",
			value: "",
			want: []dtls.SRTPProtectionProfile{
				dtls.SRTP_AES128_CM_HMAC_SHA1_80,
				dtls.SRTP_AEAD_AES_128_GCM,
			},
		},
		{
			name:  "unknown preference",
			value: "unknown",
			want: []dtls.SRTPProtectionProfile{
				dtls.SRTP_AES128_CM_HMAC_SHA1_80,
				dtls.SRTP_AEAD_AES_128_GCM,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(srtpProfilePreferenceEnv, test.value)
			got := srtpProtectionProfiles()
			if len(got) != len(test.want) {
				t.Fatalf("profiles=%v, want %v", got, test.want)
			}
			for i := range test.want {
				if got[i] != test.want[i] {
					t.Fatalf("profiles=%v, want %v", got, test.want)
				}
			}
		})
	}
}
