package webrtc

import (
	"NanoKVM-Server/service/stream"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestH265WebRTCRejectedBeforeNegotiation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/video", Connect)
	server := httptest.NewServer(router)
	defer server.Close()
	// Policy is unconditional: switching AES to software does not enable HEVC.
	for _, aes := range []string{"hardware", "software"} {
		t.Run(aes, func(t *testing.T) {
			t.Setenv("NANOKVM_SRTP_AES", aes)
			ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/video?codec=h265", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			ws.SetReadDeadline(time.Now().Add(2 * time.Second))
			var message Message
			if err := ws.ReadJSON(&message); err != nil {
				t.Fatal(err)
			}
			if message.Event != "video-error" || message.Data != h265WebRTCError {
				t.Fatalf("unexpected first message: %+v", message)
			}
			if _, _, err := ws.ReadMessage(); err == nil {
				t.Fatal("rejected connection remained open")
			}
		})
	}
}

func TestWebRTCDefaultStillNegotiatesH264(t *testing.T) {
	t.Setenv("NANOKVM_SRTP_AES", "software")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/video", Connect)
	server := httptest.NewServer(router)
	defer server.Close()
	for _, query := range []string{"", "?codec=h264"} {
		ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/video"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		var message Message
		err = ws.ReadJSON(&message)
		ws.Close()
		if err != nil || message.Event != "ice-servers" {
			t.Fatalf("query=%q message=%+v error=%v", query, message, err)
		}
	}
}

func TestH265CannotEnterWebRTCManagerOrSDP(t *testing.T) {
	config := stream.EncoderConfig{Codec: stream.VideoCodecH265}
	manager := NewWebRTCManager()
	if err := manager.AddClient(nil, &Client{config: config}); err == nil || err.Error() != h265WebRTCError {
		t.Fatalf("manager admitted HEVC: %v", err)
	}
	if manager.GetClientCount() != 0 || manager.subscription != nil {
		t.Fatal("rejected HEVC allocated a subscription")
	}
	if engine, err := createMediaEngine(config); err == nil || engine != nil {
		t.Fatal("HEVC media engine was created")
	}
}
