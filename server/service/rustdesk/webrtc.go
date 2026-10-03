// SPDX-License-Identifier: AGPL-3.0-only
package rustdesk

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/pion/webrtc/v4"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const rtcMaxMessage = 8 << 20
const rtcMaxFragment = 60000
const rtcMaxSignal = 64 << 10
const rtcMaxInbound = 256 << 10

type rtcCommand struct {
	Op        string `json:"op"`
	Offer     string `json:"offer,omitempty"`
	Token     string `json:"token,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}
type rtcEvent struct {
	Event       string `json:"event"`
	Answer      string `json:"answer,omitempty"`
	Token       string `json:"token,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	SessionKey  string `json:"session_key,omitempty"`
	Candidate   string `json:"candidate,omitempty"`
	Error       string `json:"error,omitempty"`
}
type rtcBridge struct {
	listener    net.Listener
	mu          sync.Mutex
	sessions    map[string]*rtcSession
	connections map[net.Conn]struct{}
	iceServers  []webrtc.ICEServer
	closed      bool
}
type rtcSession struct {
	pc          *webrtc.PeerConnection
	owner       *rtcBridge
	token       string
	ctx         context.Context
	cancel      context.CancelFunc
	once        sync.Once
	events      chan rtcEvent
	incoming    chan []byte
	opened      chan struct{}
	pcReady     chan struct{}
	channel     *webrtc.DataChannel
	bound       atomic.Bool
	attached    atomic.Bool
	rxMu        sync.Mutex
	fragments   []byte
	queuedBytes atomic.Int64
}

func startRTCBridge(listener net.Listener) *rtcBridge {
	b := &rtcBridge{listener: listener, sessions: make(map[string]*rtcSession),
		connections: make(map[net.Conn]struct{}),
		iceServers:  []webrtc.ICEServer{{URLs: []string{"stun:stun.cloudflare.com:3478", "stun:stun.l.google.com:19302"}}}}
	go b.accept()
	return b
}
func (b *rtcBridge) accept() {
	for {
		c, err := b.listener.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		if b.closed || len(b.connections) >= 12 {
			b.mu.Unlock()
			c.Close()
			continue
		}
		b.connections[c] = struct{}{}
		b.mu.Unlock()
		go func() { defer func() { c.Close(); b.mu.Lock(); delete(b.connections, c); b.mu.Unlock() }(); b.serve(c) }()
	}
}
func (b *rtcBridge) close() {
	b.mu.Lock()
	b.closed = true
	all := make([]*rtcSession, 0, len(b.sessions))
	for _, s := range b.sessions {
		all = append(all, s)
	}
	for c := range b.connections {
		c.Close()
	}
	b.mu.Unlock()
	b.listener.Close()
	for _, s := range all {
		s.close()
	}
}
func (s *rtcSession) close() {
	s.once.Do(func() {
		s.cancel()
		// Keep the slot until teardown finishes, including a racing setup.
		go func() {
			<-s.pcReady
			if s.pc != nil {
				_ = s.pc.Close()
			}
			s.owner.mu.Lock()
			delete(s.owner.sessions, s.token)
			s.owner.mu.Unlock()
		}()
	})
}
func (s *rtcSession) notify(e rtcEvent) {
	select {
	case s.events <- e:
	case <-s.ctx.Done():
	default:
		s.close()
	}
}
func (b *rtcBridge) serve(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var command rtcCommand
	if readRTCJSON(c, &command) != nil {
		return
	}
	if command.Op == "attach" {
		b.attach(c, command.Token)
		return
	}
	if command.Op != "offer" {
		return
	}
	s, answer, fingerprint, sessionKey, err := b.answer(command.Offer)
	if err != nil {
		_ = writeRTCJSON(c, rtcEvent{Event: "error", Error: err.Error()})
		return
	}
	defer s.close()
	if writeRTCJSON(c, rtcEvent{Event: "answer", Answer: answer, Token: s.token,
		Fingerprint: fingerprint, SessionKey: sessionKey}) != nil {
		return
	}
	c.SetReadDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var cmd rtcCommand
			if readRTCJSON(c, &cmd) != nil {
				return
			}
			if cmd.Op != "candidate" || len(cmd.Candidate) > 8192 {
				return
			}
			var candidate webrtc.ICECandidateInit
			if json.Unmarshal([]byte(cmd.Candidate), &candidate) != nil {
				return
			}
			if s.pc.AddICECandidate(candidate) != nil {
				return
			}
		}
	}()
	setup := time.NewTimer(20 * time.Second)
	defer setup.Stop()
	for {
		select {
		case e := <-s.events:
			if e.Event == "connected" {
				setup.Stop()
			}
			c.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if writeRTCJSON(c, e) != nil {
				return
			}
		case <-setup.C:
			return
		case <-done:
			return
		case <-s.ctx.Done():
			return
		}
	}
}
func (b *rtcBridge) answer(endpoint string) (*rtcSession, string, string, string, error) {
	if len(endpoint) > rtcMaxSignal || !strings.HasPrefix(endpoint, "webrtc://") {
		return nil, "", "", "", errors.New("invalid WebRTC endpoint")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(endpoint, "webrtc://"))
	if err != nil {
		return nil, "", "", "", errors.New("invalid WebRTC endpoint")
	}
	var offer webrtc.SessionDescription
	if json.Unmarshal(payload, &offer) != nil || offer.Type != webrtc.SDPTypeOffer {
		return nil, "", "", "", errors.New("invalid SDP offer")
	}
	key, err := rtcFingerprint(offer.SDP)
	if err != nil {
		return nil, "", "", "", err
	}
	b.mu.Lock()
	if b.closed || len(b.sessions) >= 4 {
		b.mu.Unlock()
		return nil, "", "", "", errors.New("too many pending WebRTC sessions")
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		b.mu.Unlock()
		return nil, "", "", "", err
	}
	token := hex.EncodeToString(tokenBytes)
	ctx, cancel := context.WithCancel(context.Background())
	s := &rtcSession{owner: b, token: token, ctx: ctx, cancel: cancel, events: make(chan rtcEvent, 32),
		incoming: make(chan []byte, 32), opened: make(chan struct{}), pcReady: make(chan struct{})}
	b.sessions[token] = s
	b.mu.Unlock()
	failed := func(err error) (*rtcSession, string, string, string, error) {
		s.close()
		return nil, "", "", "", err
	}
	setting := webrtc.SettingEngine{}
	setting.SetSCTPMaxMessageSize(65536)
	setting.SetSCTPMaxReceiveBufferSize(256 << 10)
	api := webrtc.NewAPI(webrtc.WithSettingEngine(setting))
	s.pc, err = api.NewPeerConnection(webrtc.Configuration{ICEServers: b.iceServers})
	close(s.pcReady)
	if err != nil {
		return failed(err)
	}
	if s.ctx.Err() != nil {
		return failed(errors.New("WebRTC setup canceled"))
	}
	s.pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		data, err := json.Marshal(candidate.ToJSON())
		if err == nil {
			s.notify(rtcEvent{Event: "candidate", Candidate: string(data)})
		}
	})
	s.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected {
			s.close()
		}
	})
	s.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if !dc.Ordered() || dc.MaxRetransmits() != nil || dc.MaxPacketLifeTime() != nil || !s.bound.CompareAndSwap(false, true) {
			dc.OnOpen(func() { _ = dc.Close() })
			return
		}
		s.channel = dc
		dc.OnOpen(func() { close(s.opened); s.notify(rtcEvent{Event: "connected"}) })
		dc.OnClose(s.close)
		dc.OnMessage(func(message webrtc.DataChannelMessage) {
			if message.IsString {
				s.close()
				return
			}
			s.rxMu.Lock()
			defer s.rxMu.Unlock()
			payload, complete, err := appendRTCFragment(&s.fragments, message.Data)
			if err != nil {
				s.close()
				return
			}
			if complete {
				if s.queuedBytes.Add(int64(len(payload))) > 512<<10 {
					s.close()
					return
				}
				select {
				case s.incoming <- payload:
				case <-s.ctx.Done():
				default:
					s.close()
				}
			}
		})
	})
	if err = s.pc.SetRemoteDescription(offer); err != nil {
		return failed(err)
	}
	answer, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return failed(err)
	}
	if err = s.pc.SetLocalDescription(answer); err != nil {
		return failed(err)
	}
	fingerprint, err := rtcFingerprint(answer.SDP)
	if err != nil {
		return failed(err)
	}
	data, err := json.Marshal(answer)
	if err != nil {
		return failed(err)
	}
	return s, "webrtc://" + base64.StdEncoding.EncodeToString(data), fingerprint, key, nil
}
func (b *rtcBridge) attach(c net.Conn, token string) {
	b.mu.Lock()
	s := b.sessions[token]
	b.mu.Unlock()
	if s == nil || !s.attached.CompareAndSwap(false, true) {
		return
	}
	defer s.close()
	select {
	case <-s.opened:
	case <-s.ctx.Done():
		return
	case <-time.After(20 * time.Second):
		return
	}
	if writeRTCJSON(c, rtcEvent{Event: "ready"}) != nil {
		return
	}
	c.SetReadDeadline(time.Time{})
	go func() {
		defer s.close()
		for {
			payload, err := readRustDeskPayloadBounded(c, 3*time.Second)
			if err != nil {
				return
			}
			for offset := 0; offset < len(payload) || offset == 0; {
				end := min(offset+rtcMaxFragment, len(payload))
				flag := byte(1)
				if end == len(payload) {
					flag = 0
				}
				fragment := make([]byte, 1+end-offset)
				fragment[0] = flag
				copy(fragment[1:], payload[offset:end])
				deadline := time.Now().Add(3 * time.Second)
				for s.channel.BufferedAmount() > 2<<20 {
					select {
					case <-s.ctx.Done():
						return
					case <-time.After(5 * time.Millisecond):
					}
					if time.Now().After(deadline) {
						return
					}
				}
				if s.channel.Send(fragment) != nil {
					return
				}
				if end == len(payload) {
					break
				}
				offset = end
			}
		}
	}()
	for {
		select {
		case payload := <-s.incoming:
			s.queuedBytes.Add(-int64(len(payload)))
			c.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if writeRustDeskPayload(c, payload) != nil {
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}
func rtcFingerprint(sdp string) (string, error) {
	found := ""
	for _, line := range strings.Split(sdp, "\n") {
		if strings.HasPrefix(line, "a=fingerprint:sha-256 ") {
			raw := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(line, "a=fingerprint:sha-256 ")))
			parts := strings.Split(raw, ":")
			if len(parts) != 32 {
				return "", errors.New("invalid DTLS fingerprint")
			}
			for _, p := range parts {
				if len(p) != 2 {
					return "", errors.New("invalid DTLS fingerprint")
				}
			}
			decoded, err := hex.DecodeString(strings.Join(parts, ""))
			if err != nil || len(decoded) != 32 {
				return "", errors.New("invalid DTLS fingerprint")
			}
			value := "sha-256 " + raw
			if found != "" && value != found {
				return "", errors.New("conflicting DTLS fingerprints")
			}
			found = value
		}
	}
	if found == "" {
		return "", errors.New("missing SHA-256 DTLS fingerprint")
	}
	return found, nil
}
func appendRTCFragment(buffer *[]byte, fragment []byte) ([]byte, bool, error) {
	if len(fragment) < 1 || len(fragment) > rtcMaxFragment+1 ||
		fragment[0] > 1 || (fragment[0] == 1 && len(fragment) == 1) ||
		len(*buffer)+len(fragment)-1 > rtcMaxInbound {
		return nil, false, errors.New("invalid RustDesk fragment")
	}
	*buffer = append(*buffer, fragment[1:]...)
	if fragment[0] == 1 {
		return nil, false, nil
	}
	payload := *buffer
	*buffer = nil
	return payload, true, nil
}
func readRTCJSON(r io.Reader, v any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(prefix[:])
	if n == 0 || n > rtcMaxSignal {
		return errors.New("oversized RTC command")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
func writeRTCJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > rtcMaxSignal {
		return errors.New("oversized RTC event")
	}
	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], uint32(len(data)))
	if err = writeRTCAll(w, prefix[:]); err != nil {
		return err
	}
	return writeRTCAll(w, data)
}

// Idle is allowed. Once a frame starts its remaining header/body has a deadline.
// A canceled session closes the owning IPC connection and interrupts either read.
func readRustDeskPayloadBounded(c net.Conn, timeout time.Duration) ([]byte, error) {
	var first [1]byte
	if _, err := io.ReadFull(c, first[:]); err != nil {
		return nil, err
	}
	if err := c.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	defer c.SetReadDeadline(time.Time{})
	return readRustDeskPayloadAfterHead(c, first[0])
}
func readRustDeskPayload(r io.Reader) ([]byte, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return nil, err
	}
	return readRustDeskPayloadAfterHead(r, first[0])
}
func readRustDeskPayloadAfterHead(r io.Reader, first byte) ([]byte, error) {
	var head [4]byte
	head[0] = first
	width := int(head[0]&3) + 1
	if _, err := io.ReadFull(r, head[1:width]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(head[:]) >> 2
	if n > rtcMaxMessage {
		return nil, errors.New("oversized RustDesk message")
	}
	data := make([]byte, n)
	_, err := io.ReadFull(r, data)
	return data, err
}
func writeRustDeskPayload(w io.Writer, data []byte) error {
	if len(data) > rtcMaxMessage {
		return errors.New("oversized RustDesk message")
	}
	width := 1
	for width < 4 && len(data) >= (1<<(8*width-2)) {
		width++
	}
	header := uint32(len(data))<<2 | uint32(width-1)
	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], header)
	if err := writeRTCAll(w, prefix[:width]); err != nil {
		return err
	}
	return writeRTCAll(w, data)
}

func writeRTCAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n < 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
