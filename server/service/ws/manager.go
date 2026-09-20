package ws

import (
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
	m.mutex.Unlock()
	if client != nil {
		client.setControlEnabled(false)
	}
}

// SetControl changes the single browser session allowed to send manual HID
// input. Other sessions continue receiving video and status messages.
func (m *Manager) SetControl(client *Client, enabled bool) {
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

func (m *Manager) GetClients() []*Client {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	clients := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}

	return clients
}
