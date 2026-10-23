package webrtc

import (
	"time"

	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/vm"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// Includes the base RTP header. With a 16-byte SRTP tag, UDP (8) and IPv6
// (40), this produces at most 1280 bytes, matching Tailscale's IPv6 MTU.
// Preserve this budget when adding RTP extensions or changing packetization.
const videoRTPMTU = 1216

// Seams for tests: starting, telling ended and closing a subscription, and
// telling the clients of an ended one.
var (
	subscribeVideo     = stream.SubscribeVideo
	subscriptionEnded  = (*stream.VideoSubscription).Closed
	closeSubscription  = (*stream.VideoSubscription).Close
	notifyEndedClients = func(clients []*Client) {
		for _, client := range clients {
			go func() {
				// A deadline: a client that does not read its socket must not
				// hold anything up.
				_ = client.writeMessageWithin("video-error", "encoder-reconfigured", 2*time.Second)
				client.Close()
			}()
		}
	}
)

func NewWebRTCManager() *WebRTCManager {
	m := &WebRTCManager{
		clients:      make(map[*websocket.Conn]*Client),
		writers:      make(map[*Client]*peerVideoWriter),
		videoSending: false,
	}
	m.updateClientSnapshotLocked()

	return m
}

func (m *WebRTCManager) AddClient(ws *websocket.Conn, client *Client) error {
	m.mutex.Lock()
	if _, exists := m.clients[ws]; exists {
		m.mutex.Unlock()
		return nil
	}
	// A subscription that has ended (a codec change, H.264 blocked) but whose
	// stream loop has not detached it yet: detach it here, so this client
	// starts a new one instead of joining a dead stream or meeting its old
	// configuration as a conflict.
	var ended []*Client
	var endedSubscription *stream.VideoSubscription
	var endedVersion uint64
	if m.subscription != nil && subscriptionEnded(m.subscription) {
		endedSubscription = m.subscription
		ended = m.endSubscriptionLocked(endedSubscription)
		endedVersion = m.viewerVersion
	}
	defer func() {
		if endedSubscription != nil {
			closeSubscription(endedSubscription)
			vm.UpdateHdmiViewerSnapshot("webrtc", 0, endedVersion)
			notifyEndedClients(ended)
		}
	}()
	if len(m.clients) > 0 && m.config != client.config {
		active := m.config
		m.mutex.Unlock()
		return &stream.EncoderConfigConflictError{Active: active, Requested: client.config}
	}

	if len(m.clients) == 0 {
		subscription, err := subscribeVideo(client.config)
		if err != nil {
			m.mutex.Unlock()
			return err
		}
		m.config = client.config
		m.subscription = subscription
	}

	var w *peerVideoWriter
	w = newPeerVideoWriter(func(sample stream.VideoFrame) error {
		// A reconnect may outlive an old blocked track write. Serialize only this
		// peer's packetizer; neither the manager nor another peer waits here.
		client.videoWriteMutex.Lock()
		defer client.videoWriteMutex.Unlock()
		if w.isClosed() {
			return nil
		}
		return client.writeFrame(sample)
	}, func(err error) {
		log.Errorf("failed to write video to client: %s", err)
		if m.removeClient(ws, w) {
			client.Close()
		}
	})
	m.writers[client] = w
	m.clients[ws] = client
	count := m.updateClientSnapshotLocked()
	m.viewerVersion++
	version := m.viewerVersion
	m.mutex.Unlock()
	vm.UpdateHdmiViewerSnapshot("webrtc", count, version)

	log.Debugf("added client %s, total clients: %d", ws.RemoteAddr(), count)
	return nil
}

func (m *WebRTCManager) RemoveClient(ws *websocket.Conn) {
	m.removeClient(ws, nil)
}

func (m *WebRTCManager) removeClient(ws *websocket.Conn, expected *peerVideoWriter) bool {
	m.mutex.Lock()
	if client, exists := m.clients[ws]; !exists || (expected != nil && m.writers[client] != expected) {
		m.mutex.Unlock()
		return false
	}
	client := m.clients[ws]
	if writer := m.writers[client]; writer != nil {
		writer.close()
		delete(m.writers, client)
	}
	delete(m.clients, ws)
	count := m.updateClientSnapshotLocked()
	m.viewerVersion++
	version := m.viewerVersion
	var subscription *stream.VideoSubscription
	if count == 0 {
		subscription = m.subscription
		m.subscription = nil
		m.config = stream.EncoderConfig{}
		m.videoSending = false
	}
	m.mutex.Unlock()
	if subscription != nil {
		subscription.Close()
	}
	vm.UpdateHdmiViewerSnapshot("webrtc", count, version)

	log.Debugf("removed client %s, total clients: %d", ws.RemoteAddr(), count)
	return true
}

// endSubscriptionLocked detaches an ended subscription together with every
// client on it (the map, not a snapshot, so a client that joined meanwhile
// goes too) and resets the manager, so the next client subscribes anew. The
// caller closes the subscription and tells the clients, outside the lock.
func (m *WebRTCManager) endSubscriptionLocked(subscription *stream.VideoSubscription) []*Client {
	if subscription == nil || m.subscription != subscription {
		return nil
	}
	clients := make([]*Client, 0, len(m.clients))
	for ws, client := range m.clients {
		if writer := m.writers[client]; writer != nil {
			writer.close()
			delete(m.writers, client)
		}
		delete(m.clients, ws)
		clients = append(clients, client)
	}
	m.subscription = nil
	m.config = stream.EncoderConfig{}
	m.videoSending = false
	m.updateClientSnapshotLocked()
	m.viewerVersion++
	return clients
}

func (m *WebRTCManager) updateClientSnapshotLocked() int {
	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.clientSnapshot.Store(&clients)

	return len(clients)
}

func (m *WebRTCManager) StartVideoStream() {
	m.mutex.Lock()
	if m.videoSending || len(m.clients) == 0 || m.subscription == nil {
		m.mutex.Unlock()
		return
	}
	m.videoSending = true
	subscription := m.subscription
	codec := m.config.Codec
	m.mutex.Unlock()

	go m.sendVideoStream(subscription)
	log.Debugf("start sending %s WebRTC stream", codec)
}

func (m *WebRTCManager) sendVideoStream(subscription *stream.VideoSubscription) {
	for {
		frame, ok := subscription.Next()
		if !ok {
			m.mutex.Lock()
			clients := m.endSubscriptionLocked(subscription)
			version := m.viewerVersion
			m.mutex.Unlock()
			if clients != nil {
				closeSubscription(subscription)
				vm.UpdateHdmiViewerSnapshot("webrtc", 0, version)
				notifyEndedClients(clients)
			}
			return
		}
		stream.UpdateCaptureStatus(stream.CaptureModeH264, frame.Result)
		if frame.Result < 0 || len(frame.Data) == 0 {
			continue
		}
		// VideoSource owns immutable Go frame storage. Each writer creates its
		// own RTP packets under its current PMTU, sequence and timestamp state.
		m.mutex.Lock()
		if m.subscription != subscription {
			m.mutex.Unlock()
			return
		}
		for _, writer := range m.writers {
			writer.offer(frame)
		}
		m.mutex.Unlock()
	}
}
