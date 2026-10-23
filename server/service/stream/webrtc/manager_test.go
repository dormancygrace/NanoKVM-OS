package webrtc

import (
	"testing"

	"github.com/gorilla/websocket"
)

func TestStaleWriterCannotRemoveReconnectedPeer(t *testing.T) {
	manager := NewWebRTCManager()
	ws := &websocket.Conn{}
	client := &Client{}
	oldWriter := &peerVideoWriter{}
	currentWriter := &peerVideoWriter{}
	manager.clients[ws] = client
	manager.writers[client] = currentWriter
	if manager.removeClient(ws, oldWriter) {
		t.Fatal("stale writer removed new connection")
	}
	if manager.clients[ws] != client || manager.writers[client] != currentWriter {
		t.Fatal("reconnected peer changed")
	}
}
