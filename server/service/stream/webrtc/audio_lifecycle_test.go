package webrtc

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"NanoKVM-Server/service/stream/audio"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type lifecycleAudioCapture struct {
	closed chan struct{}
	quit   chan struct{}
	once   sync.Once
}

func (s *lifecycleAudioCapture) Read(ctx context.Context) (audio.Packet, error) {
	select {
	case <-ctx.Done():
		return audio.Packet{}, ctx.Err()
	case <-s.quit:
		return audio.Packet{}, io.EOF
	}
}
func (s *lifecycleAudioCapture) Close() { s.once.Do(func() { close(s.closed) }) }

// A browser can close its page without sending audio-stop. Exercise the
// real signaling handler, including all its deferred cleanup, with idle audio.
func TestSignalingDisconnectReleasesBundledAudio(t *testing.T) {
	t.Setenv("NANOKVM_SRTP_AES", "software")
	capture := &lifecycleAudioCapture{closed: make(chan struct{}), quit: make(chan struct{})}
	original := subscribeAudio
	subscribeAudio = func() (packetSource, error) { return capture, nil }
	t.Cleanup(func() {
		close(capture.quit)
		select {
		case <-capture.closed:
		case <-time.After(time.Second):
			t.Error("audio goroutine did not exit during cleanup")
		}
		subscribeAudio = original
	})
	gin.SetMode(gin.TestMode)
	finished := make(chan struct{})
	router := gin.New()
	router.GET("/webrtc", func(c *gin.Context) {
		ConnectLegacy(c)
		close(finished)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/webrtc", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	readUntil := func(event string) {
		t.Helper()
		for {
			var reply Message
			if err := ws.ReadJSON(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.Event == "audio-error" {
				t.Fatalf("audio refused: %s", reply.Data)
			}
			if reply.Event == event {
				return
			}
		}
	}
	readUntil("ice-servers")
	browser := browserPeer(t, true)
	offer, err := browser.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteJSON(Message{Event: "video-offer", Data: string(data)}); err != nil {
		t.Fatal(err)
	}
	readUntil("video-answer")
	if err := ws.WriteJSON(Message{Event: "audio-start"}); err != nil {
		t.Fatal(err)
	}
	readUntil("audio-started")
	_ = ws.Close()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("signaling handler did not return")
	}
	select {
	case <-capture.closed:
	default:
		t.Fatal("signaling handler returned with the audio capture and listener slot still held")
	}
}
