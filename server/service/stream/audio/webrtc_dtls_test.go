package audio

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestAudioPeerAnswersAsDTLSServer(t *testing.T) {
	offerer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create offerer: %v", err)
	}
	defer offerer.Close() //nolint:errcheck
	if _, err = offerer.CreateDataChannel("audio-dtls-role", nil); err != nil {
		t.Fatalf("create data channel: %v", err)
	}
	offer, err := offerer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}

	answerer, err := createPeerConnection(nil)
	if err != nil {
		t.Fatalf("create audio peer: %v", err)
	}
	defer answerer.Close() //nolint:errcheck
	if err = answerer.SetRemoteDescription(offer); err != nil {
		t.Fatalf("set offer: %v", err)
	}
	answer, err := answerer.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("create answer: %v", err)
	}
	if !strings.Contains(answer.SDP, "a=setup:passive") {
		t.Fatalf("audio answer lacks passive DTLS role:\n%s", answer.SDP)
	}
}
