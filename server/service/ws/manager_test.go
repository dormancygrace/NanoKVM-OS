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

func TestHTTPInputFollowsSessionOwningControl(t *testing.T) {
	manager := newManager()
	if !manager.AllowsSession("anyone") {
		t.Fatal("HTTP input must work while no browser holds control")
	}
	owner := &Client{sessionID: "alice/1/1"}
	viewer := &Client{sessionID: "bob/1/2"}
	manager.AddClient(new(websocket.Conn), owner)
	manager.AddClient(new(websocket.Conn), viewer)
	if !manager.AllowsSession("alice/1/1") {
		t.Fatal("the controlling session must keep HTTP input")
	}
	if manager.AllowsSession("bob/1/2") || manager.AllowsSession("") {
		t.Fatal("view-only sessions must not inject input over HTTP")
	}
	manager.SetControl(viewer, true)
	if manager.AllowsSession("alice/1/1") || !manager.AllowsSession("bob/1/2") {
		t.Fatal("HTTP input must follow a control transfer")
	}
}
