// SPDX-License-Identifier: AGPL-3.0-only
package rustdesk

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"github.com/pion/webrtc/v4"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type rtcShortWriter struct{ bytes.Buffer }

func (w *rtcShortWriter) Write(b []byte) (int, error) { return w.Buffer.Write(b[:min(len(b), 3)]) }
func TestRustDeskRTCFraming(t *testing.T) {
	for _, n := range []int{0, 1, 63, 64, 16383, 16384, 60000, 130000, 1 << 22} {
		data := bytes.Repeat([]byte{0xa5}, n)
		var w rtcShortWriter
		if err := writeRustDeskPayload(&w, data); err != nil {
			t.Fatal(err)
		}
		got, err := readRustDeskPayload(&w)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("roundtrip %d: %v", n, err)
		}
	}
	var w rtcShortWriter
	if err := writeRTCJSON(&w, rtcEvent{Event: "ready"}); err != nil {
		t.Fatal(err)
	}
	var event rtcEvent
	if err := readRTCJSON(&w, &event); err != nil || event.Event != "ready" {
		t.Fatal(err, event)
	}
	if err := writeRustDeskPayload(io.Discard, make([]byte, rtcMaxMessage+1)); err == nil {
		t.Fatal("oversize accepted")
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(rtcMaxMessage+1)<<2|3)
	if _, err := readRustDeskPayload(bytes.NewReader(header[:])); err == nil {
		t.Fatal("oversize read")
	}
	var fragments []byte
	if _, done, err := appendRTCFragment(&fragments, append([]byte{1}, bytes.Repeat([]byte{1}, 60000)...)); err != nil || done {
		t.Fatal(err)
	}
	payload, done, err := appendRTCFragment(&fragments, []byte{0, 2})
	if err != nil || !done || len(payload) != 60001 || fragments != nil {
		t.Fatal("reassembly")
	}
	for _, bad := range [][]byte{nil, {1}, {2}, make([]byte, rtcMaxFragment+2)} {
		if _, _, err := appendRTCFragment(&fragments, bad); err == nil {
			t.Fatal("bad fragment")
		}
	}
	fragments = make([]byte, rtcMaxInbound)
	if _, _, err := appendRTCFragment(&fragments, []byte{0, 1}); err == nil {
		t.Fatal("unbounded assembly")
	}
	fp := strings.Repeat("AB:", 31) + "AB"
	if got, err := rtcFingerprint("a=fingerprint:sha-256 " + fp + "\r\n"); err != nil || got != "sha-256 "+fp {
		t.Fatal(got, err)
	}
	if _, err := rtcFingerprint("a=fingerprint:sha-256 " + fp + "\na=fingerprint:sha-256 " + strings.ReplaceAll(fp, "AB", "CD")); err == nil {
		t.Fatal("ambiguous certificate")
	}
}
func rtcTestWait(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out: " + what)
}
func TestRustDeskRTCRealDataChannelAndRPCLifetime(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Configure before publishing the listener or starting accept.
	b := &rtcBridge{listener: listener, sessions: make(map[string]*rtcSession), connections: make(map[net.Conn]struct{})}
	go b.accept()
	defer b.close()
	client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dc, err := client.CreateDataChannel("data", nil)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan []byte, 2)
	var mu sync.Mutex
	var assembled []byte
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		mu.Lock()
		defer mu.Unlock()
		if len(m.Data) < 1 || m.Data[0] > 1 {
			return
		}
		assembled = append(assembled, m.Data[1:]...)
		if m.Data[0] == 0 {
			received <- assembled
			assembled = nil
		}
	})
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(client)
	if err = client.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		t.Fatal("offer gathering")
	}
	encoded, _ := json.Marshal(client.LocalDescription())
	ctrl, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	ctrl.SetDeadline(time.Now().Add(10 * time.Second))
	if err = writeRTCJSON(ctrl, rtcCommand{Op: "offer", Offer: "webrtc://" + base64.StdEncoding.EncodeToString(encoded)}); err != nil {
		t.Fatal(err)
	}
	var answer rtcEvent
	if err = readRTCJSON(ctrl, &answer); err != nil || answer.Event != "answer" {
		t.Fatal(err, answer.Event)
	}
	if len(answer.Token) != 64 || answer.SessionKey == "" || answer.Fingerprint == "" {
		t.Fatal("missing authenticated identity")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(answer.Answer, "webrtc://"))
	if err != nil {
		t.Fatal(err)
	}
	var description webrtc.SessionDescription
	if err = json.Unmarshal(raw, &description); err != nil {
		t.Fatal(err)
	}
	if fp, err := rtcFingerprint(description.SDP); err != nil || fp != answer.Fingerprint {
		t.Fatal("identity mismatch")
	}
	if err = client.SetRemoteDescription(description); err != nil {
		t.Fatal(err)
	}
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for {
			var event rtcEvent
			if readRTCJSON(ctrl, &event) != nil {
				return
			}
			if event.Event == "candidate" {
				var candidate webrtc.ICECandidateInit
				if json.Unmarshal([]byte(event.Candidate), &candidate) == nil {
					_ = client.AddICECandidate(candidate)
				}
			}
		}
	}()
	data, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	data.SetDeadline(time.Now().Add(10 * time.Second))
	if err = writeRTCJSON(data, rtcCommand{Op: "attach", Token: answer.Token}); err != nil {
		t.Fatal(err)
	}
	var ready rtcEvent
	if err = readRTCJSON(data, &ready); err != nil || ready.Event != "ready" {
		t.Fatal("attach", err, ready.Event)
	}
	duplicate, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	duplicate.SetDeadline(time.Now().Add(time.Second))
	if err = writeRTCJSON(duplicate, rtcCommand{Op: "attach", Token: answer.Token}); err != nil {
		t.Fatal(err)
	}
	var rejected rtcEvent
	if err = readRTCJSON(duplicate, &rejected); err == nil {
		t.Fatal("duplicate attach accepted")
	}
	duplicate.Close()
	outgoing := bytes.Repeat([]byte{0x6d}, 180001)
	if err = writeRustDeskPayload(data, outgoing); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if !bytes.Equal(got, outgoing) {
			t.Fatal("fragmented outgoing corrupted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("outgoing channel")
	}
	incoming := bytes.Repeat([]byte{0xd3}, 130001)
	for offset := 0; offset < len(incoming); offset += rtcMaxFragment {
		end := min(offset+rtcMaxFragment, len(incoming))
		flag := byte(1)
		if end == len(incoming) {
			flag = 0
		}
		if err = dc.Send(append([]byte{flag}, incoming[offset:end]...)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := readRustDeskPayload(data)
	if err != nil || !bytes.Equal(got, incoming) {
		t.Fatal("incoming corrupted", err)
	}
	// Ordinary input bursts must survive the bounded queue.
	for i := 0; i < 16; i++ {
		if err = dc.Send([]byte{0, byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 16; i++ {
		got, err = readRustDeskPayload(data)
		if err != nil || len(got) != 1 || got[0] != byte(i) {
			t.Fatal("burst lost", i, err)
		}
	}
	ctrl.Close()
	rtcTestWait(t, "session cleanup", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.sessions) == 0 })
	select {
	case <-eventsDone:
	case <-time.After(time.Second):
		t.Fatal("RPC reader leak")
	}
}
func TestRustDeskRTCStopDuringSetup(t *testing.T) {
	for i := 0; i < 5; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		b := &rtcBridge{listener: listener, sessions: make(map[string]*rtcSession), connections: make(map[net.Conn]struct{})}
		client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.CreateDataChannel("data", nil)
		if err != nil {
			t.Fatal(err)
		}
		offer, err := client.CreateOffer(nil)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(offer)
		done := make(chan struct{})
		go func() {
			defer close(done)
			s, _, _, _, _ := b.answer("webrtc://" + base64.StdEncoding.EncodeToString(encoded))
			if s != nil {
				s.close()
			}
		}()
		b.close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("setup teardown hung")
		}
		rtcTestWait(t, "stopped setup cleanup", func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.sessions) == 0 })
		client.Close()
	}
}

func TestRustDeskRTCStartedFrameDeadlineAndCancellation(t *testing.T) {
	for _, partial := range [][]byte{{3, 1}, {7 << 2, 1, 2}} {
		server, client := net.Pipe()
		done := make(chan error, 1)
		go func() { _, err := readRustDeskPayloadBounded(server, 60*time.Millisecond); done <- err }()
		if _, err := client.Write(partial); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
				t.Fatal("missing partial-frame timeout", err)
			}
		case <-time.After(time.Second):
			t.Fatal("partial frame stalled")
		}
		server.Close()
		client.Close()
	}
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { _, err := readRustDeskPayloadBounded(server, 60*time.Millisecond); done <- err }()
	select {
	case err := <-done:
		t.Fatal("idle ended", err)
	case <-time.After(100 * time.Millisecond):
	}
	server.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("close accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("idle cancellation stalled")
	}

	server, client = net.Pipe()
	defer client.Close()
	go func() { _, err := readRustDeskPayloadBounded(server, time.Second); done <- err }()
	if _, err := client.Write([]byte{7 << 2, 1}); err != nil {
		t.Fatal(err)
	}
	server.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("close accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("partial cancellation stalled")
	}
}
func TestRustDeskRTCSessionAndConnectionCaps(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &rtcBridge{listener: listener, sessions: make(map[string]*rtcSession), connections: make(map[net.Conn]struct{})}
	// No PeerConnection is constructed once all four slots, including teardown, are held.
	client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.CreateDataChannel("data", nil); err != nil {
		t.Fatal(err)
	}
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(offer)
	for _, token := range []string{"a", "b", "c", "d"} {
		b.sessions[token] = nil
	}
	if _, _, _, _, err = b.answer("webrtc://" + base64.StdEncoding.EncodeToString(encoded)); err == nil {
		t.Fatal("session cap bypassed")
	}
	clear(b.sessions)
	peers := make([]net.Conn, 0, 12)
	for i := 0; i < 12; i++ {
		a, p := net.Pipe()
		b.connections[a] = struct{}{}
		peers = append(peers, p)
	}
	go b.accept()
	rejected, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	rejected.SetReadDeadline(time.Now().Add(time.Second))
	var byte [1]byte
	if _, err = rejected.Read(byte[:]); err == nil {
		t.Fatal("connection cap bypassed")
	}
	rejected.Close()
	b.close()
	for _, p := range peers {
		p.Close()
	}
}
