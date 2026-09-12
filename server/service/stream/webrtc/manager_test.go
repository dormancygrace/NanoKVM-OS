package webrtc

import (
	"NanoKVM-Server/service/stream"
	"testing"

	"github.com/gorilla/websocket"
)

func TestGetClientsForRejectsPreviousEncoderSession(t *testing.T) {
	manager := NewWebRTCManager()
	active := &stream.VideoSubscription{}
	previous := &stream.VideoSubscription{}
	connection := &websocket.Conn{}
	viewer := &Client{}

	manager.mutex.Lock()
	manager.subscription = active
	manager.clients[connection] = viewer
	manager.updateClientSnapshotLocked()
	manager.mutex.Unlock()

	if clients := manager.getClientsFor(previous); len(clients) != 0 {
		t.Fatalf("previous session can see %d active client(s)", len(clients))
	}
	if clients := manager.getClientsFor(active); len(clients) != 1 || clients[0] != viewer {
		t.Fatalf("active session clients = %v, want the current viewer", clients)
	}
}

func TestStaleWriterCannotRemoveReconnectedPeer(t *testing.T) {
 manager:=NewWebRTCManager()
 ws:=&websocket.Conn{}
 client:=&Client{}
 oldWriter:=&peerVideoWriter{}
 currentWriter:=&peerVideoWriter{}
 manager.clients[ws]=client
 manager.writers[client]=currentWriter
 if manager.removeClient(ws,oldWriter) {t.Fatal("stale writer removed new connection")}
 if manager.clients[ws]!=client || manager.writers[client]!=currentWriter {t.Fatal("reconnected peer changed")}
}
