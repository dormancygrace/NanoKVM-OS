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

func TestHTTPInputRequiresTheControllingSocketLease(t *testing.T) {
	manager := newManager()
	if !manager.AllowsInputLease("") {
		t.Fatal("HTTP input must work while no browser holds control")
	}
	// Two tabs of one login used to share an identity; each socket now has
	// its own lease.
	owner := &Client{inputLease: newInputLease()}
	sameLoginTab := &Client{inputLease: newInputLease()}
	manager.AddClient(new(websocket.Conn), owner)
	manager.AddClient(new(websocket.Conn), sameLoginTab)
	if !manager.AllowsInputLease(owner.inputLease) {
		t.Fatal("the controlling socket must keep HTTP input")
	}
	if manager.AllowsInputLease(sameLoginTab.inputLease) || manager.AllowsInputLease("") || manager.AllowsInputLease("guess") {
		t.Fatal("other sockets must not inject input over HTTP")
	}
	manager.SetControl(sameLoginTab, true)
	if manager.AllowsInputLease(owner.inputLease) || !manager.AllowsInputLease(sameLoginTab.inputLease) {
		t.Fatal("HTTP input must follow a control transfer")
	}
	if newInputLease() == newInputLease() {
		t.Fatal("leases must be random")
	}
}
