package ws

import (
	"testing"

	"github.com/gorilla/websocket"
)

func TestManagerOnlyGrantsManualControlToOneClient(t *testing.T) {
	manager := newManager()
	first := &Client{}
	second := &Client{}
	firstConnection := new(websocket.Conn)
	secondConnection := new(websocket.Conn)

	manager.AddClient(firstConnection, first)
	manager.AddClient(secondConnection, second)
	if !manager.CanControl(first) || manager.CanControl(second) {
		t.Fatal("first client should own control and later clients should start view-only")
	}
	if !first.controlEnabled || second.controlEnabled {
		t.Fatal("control status should match the initial owner")
	}

	manager.SetControl(second, true)
	if manager.CanControl(first) || !manager.CanControl(second) {
		t.Fatal("explicit control request should transfer ownership")
	}
	if first.controlEnabled || !second.controlEnabled {
		t.Fatal("control status should follow the transfer")
	}

	manager.RemoveClient(secondConnection)
	if manager.CanControl(second) {
		t.Fatal("disconnecting the owner should leave control unowned")
	}
	if second.controlEnabled {
		t.Fatal("disconnecting the owner should disable its input")
	}
}
