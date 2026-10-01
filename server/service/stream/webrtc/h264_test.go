package webrtc

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestSignalingReadLimit(t *testing.T) {
	// No media offer is made: only the actual signaling handler is exercised.
	t.Setenv("NANOKVM_SRTP_AES", "software")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/webrtc", ConnectLegacy)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	dialer := websocket.Dialer{WriteBufferSize: 1024}
	ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/webrtc", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var message Message
	if err := ws.ReadJSON(&message); err != nil || message.Event != "ice-servers" {
		t.Fatalf("initial message = %+v, error = %v", message, err)
	}
	const empty = `{"event":"heartbeat","data":""}`
	payload := `{"event":"heartbeat","data":"` + strings.Repeat("x", maxSignalingSize-len(empty)) + `"}`
	if err := ws.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := ws.ReadJSON(&message); err != nil || message.Event != "heartbeat" {
		t.Fatalf("boundary heartbeat = %+v, error = %v", message, err)
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte(payload+" ")); err != nil {
		t.Fatal(err)
	}
	_, _, err = ws.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("oversized close error = %v, want message too big", err)
	}
}
