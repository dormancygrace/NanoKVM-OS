package webrtc

import (
	"strings"
	"testing"

	"NanoKVM-Server/service/stream"

	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

// browserPeer models the web client: receive-only video, optionally audio.
func browserPeer(t *testing.T, withAudio bool) *webrtc.PeerConnection {
	t.Helper()
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(m)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	if withAudio {
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
	}
	return pc
}

func serverClient(t *testing.T) *Client {
	t.Helper()
	config := stream.DefaultEncoderConfig()
	mediaEngine, err := createMediaEngine(config)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := createPeerConnection(nil, mediaEngine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	client := NewClient(nil, pc, config)
	if err := client.AddTrack(); err != nil {
		t.Fatal(err)
	}
	return client
}

// negotiate runs one browser offer through the server path of handleVideoOffer.
func negotiate(t *testing.T, browser *webrtc.PeerConnection, client *Client) *sdp.SessionDescription {
	t.Helper()
	offer, err := browser.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := browser.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	if err := client.video.SetRemoteDescription(offer); err != nil {
		t.Fatal(err)
	}
	if err := client.attachAudioTrack(); err != nil {
		t.Fatal(err)
	}
	answer, err := client.video.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.video.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	if err := browser.SetRemoteDescription(answer); err != nil {
		t.Fatal(err)
	}
	parsed := &sdp.SessionDescription{}
	if err := parsed.UnmarshalString(answer.SDP); err != nil {
		t.Fatal(err)
	}
	return parsed
}

func audioMedia(t *testing.T, d *sdp.SessionDescription) *sdp.MediaDescription {
	t.Helper()
	for _, m := range d.MediaDescriptions {
		if m.MediaName.Media == "audio" {
			return m
		}
	}
	return nil
}

func hasAttr(m *sdp.MediaDescription, key string) bool {
	_, ok := m.Attribute(key)
	return ok
}

func TestAudioJoinsVideoConnection(t *testing.T) {
	browser, client := browserPeer(t, true), serverClient(t)
	answer := negotiate(t, browser, client)
	audio := audioMedia(t, answer)
	if audio == nil || audio.MediaName.Port.Value == 0 {
		t.Fatalf("answer has no accepted audio m-line:\n%v", answer)
	}
	if !hasAttr(audio, "sendonly") {
		t.Fatalf("audio is not sendonly")
	}
	msid, _ := audio.Attribute("msid")
	if !strings.HasPrefix(msid, mediaStreamID+" ") {
		t.Fatalf("audio msid %q, want stream %q", msid, mediaStreamID)
	}
	for _, m := range answer.MediaDescriptions {
		if m.MediaName.Media == "video" {
			if v, _ := m.Attribute("msid"); !strings.HasPrefix(v, mediaStreamID+" ") {
				t.Fatalf("video msid %q, want stream %q", v, mediaStreamID)
			}
		}
	}
	first := client.audio.track
	if first == nil {
		t.Fatal("no audio track attached")
	}

	// A renegotiation keeps the same track and transceiver.
	answer = negotiate(t, browser, client)
	audio = audioMedia(t, answer)
	if audio == nil || !hasAttr(audio, "sendonly") || client.audio.track != first {
		t.Fatalf("renegotiation changed the audio: track %p -> %p\n%v", first, client.audio.track, answer)
	}
	n := 0
	for _, tr := range client.video.GetTransceivers() {
		if tr.Kind() == webrtc.RTPCodecTypeAudio {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d audio transceivers after renegotiation, want 1", n)
	}
}

func TestVideoOnlyOfferHasNoAudio(t *testing.T) {
	browser, client := browserPeer(t, false), serverClient(t)
	answer := negotiate(t, browser, client)
	if audioMedia(t, answer) != nil || client.audio.track != nil {
		t.Fatalf("video-only offer attached audio:\n%v", answer)
	}
	if err := client.startAudio(); err != errAudioNotNegotiated {
		t.Fatalf("startAudio() = %v, want errAudioNotNegotiated", err)
	}
}

func TestAudioDelayBounds(t *testing.T) {
	for _, c := range []struct {
		value string
		want  int64
	}{{"", 0}, {"x", 0}, {"-5", 0}, {"80", 80}, {"9999", 500}} {
		t.Setenv("NANOKVM_WEBRTC_AUDIO_DELAY_MS", c.value)
		if got := audioDelay().Milliseconds(); got != c.want {
			t.Errorf("delay %q = %d ms, want %d", c.value, got, c.want)
		}
	}
}
