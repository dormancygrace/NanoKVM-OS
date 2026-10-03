package ws

import (
	"crypto/subtle"
	"sync"

	"github.com/gorilla/websocket"
)

var (
	globalManager *Manager
	managerOnce   sync.Once
)

func GetManager() *Manager {
	managerOnce.Do(func() {
		globalManager = newManager()
	})
	return globalManager
}

func newManager() *Manager {
	return &Manager{clients: make(map[*websocket.Conn]*Client)}
}

func (m *Manager) AddClient(ws *websocket.Conn, client *Client) {
	m.controlMutex.Lock()
	defer m.controlMutex.Unlock()
	m.mutex.Lock()
	m.clients[ws] = client
	if m.controller == nil {
		m.controller = client
	}
	enabled := m.controller == client
	m.mutex.Unlock()
	client.setControlEnabled(enabled)
}

func (m *Manager) RemoveClient(ws *websocket.Conn) {
	m.controlMutex.Lock()
	defer m.controlMutex.Unlock()
	m.mutex.Lock()
	client := m.clients[ws]
	delete(m.clients, ws)
	wasController := client != nil && m.controller == client
	if wasController {
		m.controller = nil
		// While ownership is absent, a previously admitted manual report cannot
		// pass the post-reservation ownership check in Client.queueManualReport.
		client.revokeInput()
	}
	var next *Client
	if client != nil && m.controller == nil && len(m.clients) == 1 {
		for _, remaining := range m.clients {
			if !remaining.manualViewOnly {
				next = remaining
				m.controller = next
			}
		}
	}
	m.mutex.Unlock()
	if client != nil {
		client.setControlEnabled(false)
	}
	if next != nil {
		next.setControlEnabled(true)
	}
}

// SetControl changes the single browser session allowed to send manual HID
// input. Other sessions continue receiving video and status messages.
func (m *Manager) SetControl(client *Client, enabled bool) {
	m.controlMutex.Lock()
	defer m.controlMutex.Unlock()
	m.mutex.Lock()
	registered := false
	for _, connected := range m.clients {
		if connected == client {
			registered = true
			break
		}
	}
	if !registered {
		m.mutex.Unlock()
		client.setControlEnabled(false)
		return
	}
	client.manualViewOnly = !enabled
	if !enabled {
		if m.controller != client {
			m.mutex.Unlock()
			client.setControlEnabled(false)
			return
		}
		m.controller = nil
		client.revokeInput()
		m.mutex.Unlock()
		client.setControlEnabled(false)
		return
	}

	previous := m.controller
	if previous == client {
		m.mutex.Unlock()
		client.setControlEnabled(true)
		return
	}
	if previous != nil {
		previous.revokeInput()
	}
	m.controller = client
	m.mutex.Unlock()

	if previous != nil {
		previous.setControlEnabled(false)
	}
	client.setControlEnabled(true)
}

func (m *Manager) CanControl(client *Client) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.controller == client
}

// AllowsInputLease reports whether an HTTP input request (paste, ATX) may act:
// either no browser holds input control, or the request carries the lease of
// the socket that does. Other tabs, other logins and view-only sessions never
// receive that lease.
func (m *Manager) AllowsInputLease(lease string) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	if m.controller == nil {
		return true
	}
	owner := m.controller.inputLease
	return lease != "" && owner != "" && subtle.ConstantTimeCompare([]byte(lease), []byte(owner)) == 1
}

func (m *Manager) GetClients() []*Client {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	clients := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}

	return clients
}
