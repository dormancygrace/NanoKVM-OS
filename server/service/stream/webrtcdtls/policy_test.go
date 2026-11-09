package webrtcdtls

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/dtls/v3/pkg/protocol/handshake"
	"github.com/pion/webrtc/v4"
)

func TestCipherSuitesPreserveCertificateCompatibility(t *testing.T) {
	want := []dtls.CipherSuiteID{
		dtls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		dtls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		dtls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	}
	got := CipherSuites()
	if len(got) != len(want) {
		t.Fatalf("cipher suite count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cipher suite[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestAESPreferenceSelectsAES(t *testing.T) {
	SetPreference(func() string { return PreferAESGCM })
	defer SetPreference(func() string { return PreferChaCha20 })
	if got := CipherSuites()[0]; got != dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 {
		t.Fatalf("first suite %s", got)
	}
	both := []dtls.CipherSuiteID{
		dtls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	}
	if got := negotiateConfiguredAnswer(t, both); got != dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 {
		t.Fatalf("negotiated %s, want AES-128-GCM", got)
	}
	if got := negotiateConfiguredAnswer(t, both[:1]); got != dtls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256 {
		t.Fatalf("ChaCha20-only client: %s", got)
	}
}

func negotiateConfiguredAnswer(t *testing.T, clientSuites []dtls.CipherSuiteID) dtls.CipherSuiteID {
	t.Helper()

	offerSettings := webrtc.SettingEngine{}
	offerSettings.SetDTLSCipherSuites(clientSuites...)
	offerer, err := webrtc.NewAPI(webrtc.WithSettingEngine(offerSettings)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create offerer: %v", err)
	}
	defer offerer.Close() //nolint:errcheck
	if _, err = offerer.CreateDataChannel("configured-dtls", nil); err != nil {
		t.Fatalf("create data channel: %v", err)
	}

	answerSettings := webrtc.SettingEngine{}
	if err = Configure(&answerSettings); err != nil {
		t.Fatalf("configure answerer: %v", err)
	}
	var selectedCipher atomic.Uint32
	answerSettings.SetDTLSServerHelloMessageHook(func(message handshake.MessageServerHello) handshake.Message {
		if message.CipherSuiteID != nil {
			selectedCipher.Store(uint32(*message.CipherSuiteID))
		}
		return &message
	})
	answerer, err := webrtc.NewAPI(webrtc.WithSettingEngine(answerSettings)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create answerer: %v", err)
	}
	defer answerer.Close() //nolint:errcheck

	connected := make(chan struct{})
	var connectedOnce sync.Once
	answerer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			connectedOnce.Do(func() { close(connected) })
		}
	})

	offer, err := offerer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	offerGatheringComplete := webrtc.GatheringCompletePromise(offerer)
	if err = offerer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local offer: %v", err)
	}
	<-offerGatheringComplete
	if err = answerer.SetRemoteDescription(*offerer.LocalDescription()); err != nil {
		t.Fatalf("set remote offer: %v", err)
	}

	answer, err := answerer.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("create answer: %v", err)
	}
	answerGatheringComplete := webrtc.GatheringCompletePromise(answerer)
	if err = answerer.SetLocalDescription(answer); err != nil {
		t.Fatalf("set local answer: %v", err)
	}
	<-answerGatheringComplete
	if err = offerer.SetRemoteDescription(*answerer.LocalDescription()); err != nil {
		t.Fatalf("set remote answer: %v", err)
	}

	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for configured answerer DTLS connection")
	}
	return dtls.CipherSuiteID(selectedCipher.Load())
}

//nolint:errcheck

//nolint:errcheck
