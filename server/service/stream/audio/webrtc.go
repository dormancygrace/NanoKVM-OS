package audio

import (
	"NanoKVM-Server/config"
	"NanoKVM-Server/middleware"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"net/http"
	"sync/atomic"
	"time"
)

type signal struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

var sessions atomic.Int32

func Status(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"audio": enabled(), "bitrate": 192000, "complexity": 3}})
}
func iceServers() []webrtc.ICEServer {
	servers := []webrtc.ICEServer{}
	conf := config.GetInstance()
	if conf.Stun != "" && conf.Stun != "disable" {
		servers = append(servers, webrtc.ICEServer{URLs: []string{"stun:" + conf.Stun}})
	}
	if conf.Turn.TurnAddr != "" && conf.Turn.TurnUser != "" && conf.Turn.TurnCred != "" {
		servers = append(servers, webrtc.ICEServer{URLs: []string{"turn:" + conf.Turn.TurnAddr}, Username: conf.Turn.TurnUser, Credential: conf.Turn.TurnCred})
	}
	return servers
}
func Connect(c *gin.Context) {
	if !enabled() {
		c.String(http.StatusConflict, "USB audio is disabled")
		return
	}
	if sessions.Add(1) > 8 {
		sessions.Add(-1)
		c.String(http.StatusServiceUnavailable, "too many audio sessions")
		return
	}
	defer sessions.Add(-1)
	upgrade := websocket.Upgrader{CheckOrigin: middleware.CheckWebSocketOrigin}
	ws, err := upgrade.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	stopWatcher := middleware.WatchWebSocket(c.Request.Context(), ws)
	defer stopWatcher()
	servers := iceServers()
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: servers})
	if err != nil {
		return
	}
	defer peer.Close()
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		SDPFmtpLine: "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1;maxaveragebitrate=192000",
	}, "audio", "nanokvm-usb-audio")
	if err != nil {
		return
	}
	sender, err := peer.AddTrack(track)
	if err != nil {
		return
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()
	outgoing := make(chan signal, 32)
	incoming := make(chan signal, 16)
	readerDone := make(chan struct{})
	sessionDone := make(chan struct{})
	defer close(sessionDone)
	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		data, _ := json.Marshal(candidate.ToJSON())
		select {
		case outgoing <- signal{Event: "candidate", Data: data}:
		case <-sessionDone:
		}
	})
	ws.SetReadLimit(64 * 1024)
	_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(30 * time.Second)) })
	go func() {
		defer close(readerDone)
		for {
			var msg signal
			if ws.ReadJSON(&msg) != nil {
				return
			}
			_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
			select {
			case incoming <- msg:
			case <-sessionDone:
				return
			}
		}
	}()
	write := func(event string, data any) error {
		_ = ws.SetWriteDeadline(time.Now().Add(time.Second))
		return ws.WriteJSON(gin.H{"event": event, "data": data})
	}
	if write("ice-servers", servers) != nil {
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var client *listener
	defer func() {
		if client != nil {
			shared.remove(client)
		}
	}()
	var frames <-chan packet
	var captureDone <-chan struct{}
	var last uint64
	haveLast := false
	offered := false
	started := time.Now()
	for {
		select {
		case <-readerDone:
			return
		case <-captureDone:
			return
		case <-tick.C:
			if !enabled() {
				return
			}
			state := peer.ConnectionState()
			if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
				return
			}
			if client == nil && state == webrtc.PeerConnectionStateConnected {
				client, err = shared.add()
				if err != nil {
					_ = write("error", err.Error())
					return
				}
				frames = client.frames
				captureDone = client.done
			}
			if client == nil && time.Since(started) > 30*time.Second {
				return
			}
			if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)) != nil {
				return
			}
		case msg := <-outgoing:
			if write(msg.Event, msg.Data) != nil {
				return
			}
		case msg := <-incoming:
			switch msg.Event {
			case "offer":
				if offered {
					return
				}
				offered = true
				var offer webrtc.SessionDescription
				if json.Unmarshal(msg.Data, &offer) != nil || offer.Type != webrtc.SDPTypeOffer {
					return
				}
				if peer.SetRemoteDescription(offer) != nil {
					return
				}
				answer, e := peer.CreateAnswer(nil)
				if e != nil {
					return
				}
				if peer.SetLocalDescription(answer) != nil {
					return
				}
				if write("answer", answer) != nil {
					return
				}
			case "candidate":
				if !offered {
					return
				}
				var candidate webrtc.ICECandidateInit
				if json.Unmarshal(msg.Data, &candidate) != nil || peer.AddICECandidate(candidate) != nil {
					return
				}
			default:
				return
			}
		case frame := <-frames:
			dropped := uint64(0)
			if haveLast && frame.index > last+1 {
				dropped = frame.index - last - 1
			}
			if dropped > 65535 {
				dropped = 65535
			}
			if track.WriteSample(media.Sample{Data: frame.data, Duration: 20 * time.Millisecond, PrevDroppedPackets: uint16(dropped)}) != nil {
				return
			}
			last = frame.index
			haveLast = true
		}
	}
}
