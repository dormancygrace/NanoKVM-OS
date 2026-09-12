package webrtc

import (
	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/vm"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	log "github.com/sirupsen/logrus"
)

// Includes the base RTP header. With a 16-byte SRTP tag, UDP (8) and IPv6
// (40), this produces at most 1280 bytes, matching Tailscale's IPv6 MTU.
// Preserve this budget when adding RTP extensions or changing packetization.
const videoRTPMTU = 1216

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
	if len(m.clients) > 0 && m.config != client.config {
		active := m.config
		m.mutex.Unlock()
		return &stream.EncoderConfigConflictError{Active: active, Requested: client.config}
	}

	if len(m.clients) == 0 {
		subscription, err := stream.SubscribeVideo(client.config)
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
		packets := client.packetizer.packetize(sample.Data, sample.Timestamp, client.pathMTU.size())
		if len(packets) == 0 {
			return errVideoBudget
		}
		return client.track.writeVideoPackets(packets)
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

func (m *WebRTCManager) GetClientCount() int {
	return len(m.getClients())
}

func (m *WebRTCManager) updateClientSnapshotLocked() int {
	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.clientSnapshot.Store(&clients)

	return len(clients)
}

func (m *WebRTCManager) getClients() []*Client {
	clients := m.clientSnapshot.Load()
	if clients == nil {
		return nil
	}

	return *clients
}

func (m *WebRTCManager) getClientsFor(subscription *stream.VideoSubscription) []*Client {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.subscription != subscription {
		return nil
	}

	clients := m.clientSnapshot.Load()
	if clients == nil {
		return nil
	}
	return *clients
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

func newVideoPacketizer(codec stream.VideoCodec) rtp.Packetizer {
	payloader := rtp.Payloader(&codecs.H264Payloader{})
	if codec == stream.VideoCodecH265 {
		payloader = &codecs.H265Payloader{}
	}
	return rtp.NewPacketizer(
		videoRTPMTU,
		100,
		0x1234ABCD,
		payloader,
		rtp.NewRandomSequencer(),
		90000,
	)
}
