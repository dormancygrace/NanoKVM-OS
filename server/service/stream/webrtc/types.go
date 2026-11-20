package webrtc

import (
	"NanoKVM-Server/service/stream"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type WebRTCManager struct {
	clients        map[*websocket.Conn]*Client
	writers        map[*Client]*peerVideoWriter
	clientSnapshot atomic.Pointer[[]*Client]
	config         stream.EncoderConfig
	subscription   *stream.VideoSubscription
	videoSending   bool
	mutex          sync.Mutex
	viewerVersion  uint64
}

type Client struct {
	videoWriteMutex sync.Mutex
	pathMTU         *peerPathMTU
	sendErrors      sendErrorLog
	packetizer      *adaptiveVideoPacketizer
	ws              *websocket.Conn
	video           *webrtc.PeerConnection
	track           *Track
	config          stream.EncoderConfig
	audio           peerAudio
	mutex           sync.Mutex
}

func (c *Client) WsConn() *websocket.Conn {
	return c.ws
}

type SignalingHandler struct {
	client *Client
	mutex  sync.Mutex
	closed bool
	// connected: the peer connection carries media. paused: the viewer asked
	// for no video for now (its tab is hidden); it is a viewer only while
	// connected and not paused.
	connected bool
	paused    bool
}

type Track struct {
	video *webrtc.TrackLocalStaticRTP
}

type Message struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}
