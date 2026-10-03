package rustdesk

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"NanoKVM-Server/service/stream/audio"
)

type testAudioSource struct {
	frames chan audio.Packet
	closed chan struct{}
	once   sync.Once
}

func (s *testAudioSource) Read(ctx context.Context) (audio.Packet, error) {
	select {
	case p := <-s.frames:
		return p, nil
	case <-ctx.Done():
		return audio.Packet{}, ctx.Err()
	}
}
func (s *testAudioSource) Close() { s.once.Do(func() { close(s.closed) }) }
func audioTestPair(t *testing.T, b *Bridge, request string) (net.Conn, <-chan struct{}) {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); defer server.Close(); b.serveAudio(server) }()
	_ = client.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(client, request+"\n"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("audio handler leaked")
		}
	})
	return client, done
}
func readTestAudio(t *testing.T, c net.Conn) ([16]byte, []byte) {
	t.Helper()
	var h [16]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, int(binary.BigEndian.Uint16(h[12:14])))
	if _, err := io.ReadFull(c, data); err != nil {
		t.Fatal(err)
	}
	return h, data
}

func TestAudioInfoAndDisabledSourceNeverStartCapture(t *testing.T) {
	for _, tc := range []struct {
		enabled bool
		command string
		codec   byte
	}{{true, "info", 1}, {false, "info", 0}, {false, "opus", 0}} {
		b := NewBridge()
		b.audioEnabled = func() bool { return tc.enabled }
		b.audioSubscribe = func() (audioSource, error) { t.Error("unexpected capture subscription"); return nil, io.EOF }
		c, _ := audioTestPair(t, b, `{"version":1,"audio":"`+tc.command+`"}`)
		h, data := readTestAudio(t, c)
		if string(h[:4]) != "OKAF" || h[4] != 1 || h[5] != tc.codec || len(data) != 0 {
			t.Fatal(h, data)
		}
		if tc.enabled && (h[6] != 2 || binary.BigEndian.Uint32(h[8:12]) != 48000) {
			t.Fatal("wrong Opus format", h)
		}
	}
}

func TestAudioOpusPassthroughAndDisconnectCleanupWithoutPCM(t *testing.T) {
	source := &testAudioSource{frames: make(chan audio.Packet, 4), closed: make(chan struct{})}
	b := NewBridge()
	b.audioEnabled = func() bool { return true }
	b.audioSubscribe = func() (audioSource, error) { return source, nil }
	c, done := audioTestPair(t, b, `{"version":1,"audio":"opus"}`)
	_, data := readTestAudio(t, c)
	if len(data) != 0 {
		t.Fatal("format contains payload")
	}
	packet := []byte{0xfc, 0xff, 0xfe}
	source.frames <- audio.Packet{Data: packet, Index: 42}
	_, data = readTestAudio(t, c)
	if string(data) != string(packet) {
		t.Fatal("Opus packet was altered")
	}
	c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle capture not cancelled by peer close")
	}
	select {
	case <-source.closed:
	default:
		t.Fatal("capture subscription not released")
	}
}

func TestAudioRejectsMalformedRequestsAndPackets(t *testing.T) {
	var subscribed atomic.Int32
	for _, request := range []string{`{"version":2,"audio":"opus"}`, `{"version":1,"audio":"bad"}`, `{"version":1,"audio":"opus","extra":1}`, `{"version":1,"audio":"opus"} {}`} {
		b := NewBridge()
		b.audioEnabled = func() bool { return true }
		b.audioSubscribe = func() (audioSource, error) { subscribed.Add(1); return nil, io.EOF }
		c, _ := audioTestPair(t, b, request)
		var h [16]byte
		if _, err := io.ReadFull(c, h[:]); err == nil {
			t.Fatal("malformed request accepted")
		}
	}
	if subscribed.Load() != 0 {
		t.Fatal("bad request started capture")
	}
	for _, data := range [][]byte{nil, make([]byte, maxAudioPacket+1)} {
		s := &testAudioSource{frames: make(chan audio.Packet, 1), closed: make(chan struct{})}
		b := NewBridge()
		b.audioEnabled = func() bool { return true }
		b.audioSubscribe = func() (audioSource, error) { return s, nil }
		c, done := audioTestPair(t, b, `{"version":1,"audio":"opus"}`)
		readTestAudio(t, c)
		s.frames <- audio.Packet{Data: data}
		var one [1]byte
		if _, err := c.Read(one[:]); err == nil {
			t.Fatal("malformed frame accepted")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("handler did not close")
		}
	}
}

func TestAudioConnectionLimitAndAppStopCancelIdlePeers(t *testing.T) {
	b := NewBridge()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b.audio = listener
	go b.acceptAudio(listener)
	t.Cleanup(b.Stop)
	waitCount := func(want int) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			b.mu.Lock()
			n := len(b.audioConnections)
			b.mu.Unlock()
			if n == want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("audio connections did not reach %d", want)
	}
	var clients []net.Conn
	for i := 0; i < 8; i++ {
		c, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
		defer c.Close()
	}
	waitCount(8)
	excess, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer excess.Close()
	excess.SetReadDeadline(time.Now().Add(time.Second))
	var one [1]byte
	if _, err := excess.Read(one[:]); err != io.EOF {
		t.Fatal("excess peer not closed", err)
	}
	b.Stop()
	for _, c := range clients {
		c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := c.Read(one[:]); err != io.EOF {
			t.Fatal("idle peer not cancelled by Stop", err)
		}
	}
	waitCount(0)
}
