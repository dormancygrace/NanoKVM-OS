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
		t.Fatal("disconnecting the owner must revoke its control")
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

func TestRemainingViewerReceivesControl(t *testing.T) {
	m := newManager()
	a, b := &Client{inputLease: newInputLease()}, &Client{inputLease: newInputLease()}
	ca, cb := new(websocket.Conn), new(websocket.Conn)
	m.AddClient(ca, a)
	m.AddClient(cb, b)
	m.RemoveClient(ca)
	if !m.CanControl(b) || !b.controlEnabled || !m.AllowsInputLease(b.inputLease) || m.AllowsInputLease(a.inputLease) {
		t.Fatal("sole viewer must receive ownership, notification state and HTTP lease")
	}
}
func TestAutomaticControlWaitsForOneRemainingViewer(t *testing.T) {
	m := newManager()
	a, b, c := &Client{}, &Client{}, &Client{}
	ca, cb, cc := new(websocket.Conn), new(websocket.Conn), new(websocket.Conn)
	m.AddClient(ca, a)
	m.AddClient(cb, b)
	m.AddClient(cc, c)
	m.RemoveClient(ca)
	if m.CanControl(b) || m.CanControl(c) {
		t.Fatal("must not choose between multiple viewers")
	}
	m.RemoveClient(cb)
	if !m.CanControl(c) || !c.controlEnabled {
		t.Fatal("last remaining viewer must receive control")
	}
}
func TestExplicitViewOnlySurvivesOtherDisconnect(t *testing.T) {
	m := newManager()
	a, b := &Client{}, &Client{}
	ca, cb := new(websocket.Conn), new(websocket.Conn)
	m.AddClient(ca, a)
	m.AddClient(cb, b)
	m.SetControl(b, false)
	m.RemoveClient(ca)
	if m.CanControl(b) || b.controlEnabled {
		t.Fatal("manual view-only must be retained")
	}
	m.SetControl(b, true)
	if !m.CanControl(b) || !b.controlEnabled {
		t.Fatal("explicit take must still work")
	}
	m.SetControl(b, false)
	m.RemoveClient(ca)
	if m.CanControl(b) {
		t.Fatal("repeated disconnect must not undo manual lock")
	}
}

// Browser takeover must run external cleanup with ownership absent, before
// giving a new browser permission to inject reports.
func TestExternalInputAndBrowserTakeover(t *testing.T) {
	m := newManager()
	first, second := &Client{inputLease: newInputLease()}, &Client{inputLease: newInputLease()}
	cleaned := false
	if !m.AcquireExternalInput("external", func() {
		if m.externalLease != "" || m.CanControl(first) || m.AllowsInputLease("") || m.AllowsInputLease("external") {
			t.Fatal("cleanup must happen before browser control")
		}
		cleaned = true
		m.ReleaseExternalInput("external")
	}) {
		t.Fatal("external reservation failed")
	}
	if !m.AllowsInputLease("external") || m.AllowsInputLease("") || m.AcquireExternalInput("other", func() {}) {
		t.Fatal("single-owner check failed")
	}
	c1, c2 := new(websocket.Conn), new(websocket.Conn)
	m.AddClient(c1, first)
	m.AddClient(c2, second)
	m.RemoveClient(c2)
	if m.CanControl(first) || first.controlEnabled || cleaned {
		t.Fatal("joining/disconnecting browser stole external control")
	}
	m.SetControl(first, true)
	if !cleaned || !m.CanControl(first) || !m.AllowsInputLease(first.inputLease) || m.AllowsInputLease("external") {
		t.Fatal("takeover failed")
	}
	if m.AcquireExternalInput("other", func() {}) {
		t.Fatal("browser control must block external input")
	}
}
