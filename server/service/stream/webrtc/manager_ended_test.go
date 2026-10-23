package webrtc

import (
	"NanoKVM-Server/service/stream"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// testConn is the server end of a real WebSocket, as AddClient gets one.
func testConn(t *testing.T) *websocket.Conn {
	t.Helper()
	conns := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		conns <- conn
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	conn := <-conns
	t.Cleanup(func() { conn.Close() })
	return conn
}

// The fake subscriptions here are zero values: a stream loop started on one
// (direct's run, a client's writeLoop) blocks for the rest of the test
// binary. That is harmless and deliberate; the tests check the manager's
// state, not the loops.
func fakeEndedSubscriptions(t *testing.T, ended map[*stream.VideoSubscription]bool) (*[]*Client, *[]*stream.VideoSubscription) {
	t.Helper()
	var notified []*Client
	var started []*stream.VideoSubscription
	previousSubscribe, previousEnded, previousNotify, previousClose := subscribeVideo, subscriptionEnded, notifyEndedClients, closeSubscription
	closeSubscription = func(s *stream.VideoSubscription) { ended[s] = true }
	subscribeVideo = func(stream.EncoderConfig) (*stream.VideoSubscription, error) {
		subscription := &stream.VideoSubscription{}
		started = append(started, subscription)
		return subscription, nil
	}
	subscriptionEnded = func(s *stream.VideoSubscription) bool { return ended[s] }
	notifyEndedClients = func(clients []*Client) { notified = append(notified, clients...) }
	t.Cleanup(func() {
		subscribeVideo, subscriptionEnded, notifyEndedClients, closeSubscription = previousSubscribe, previousEnded, previousNotify, previousClose
	})
	return &notified, &started
}

func addViewer(t *testing.T, m *WebRTCManager, subscription *stream.VideoSubscription, config stream.EncoderConfig) (*websocket.Conn, *Client) {
	ws, client := testConn(t), &Client{config: config}
	m.mutex.Lock()
	m.subscription, m.config, m.videoSending = subscription, config, true
	m.clients[ws] = client
	m.writers[client] = newPeerVideoWriter(func(stream.VideoFrame) error { return nil }, func(error) {})
	m.updateClientSnapshotLocked()
	m.mutex.Unlock()
	return ws, client
}

// The ended subscription goes with every client on it, also one that joined
// after the stream loop took its snapshot, and the manager starts over.
func TestAnEndedSubscriptionTakesEveryClient(t *testing.T) {
	fakeEndedSubscriptions(t, map[*stream.VideoSubscription]bool{})
	m := NewWebRTCManager()
	old := &stream.VideoSubscription{}
	h264 := stream.EncoderConfig{Codec: stream.VideoCodecH264}
	_, first := addViewer(t, m, old, h264)
	_, late := addViewer(t, m, old, h264)
	m.mutex.Lock()
	clients := m.endSubscriptionLocked(old)
	m.mutex.Unlock()
	if len(clients) != 2 || (clients[0] != first && clients[1] != first) || (clients[0] != late && clients[1] != late) {
		t.Fatalf("detached %v", clients)
	}
	if m.subscription != nil || len(m.clients) != 0 || len(m.writers) != 0 || m.videoSending || m.config != (stream.EncoderConfig{}) {
		t.Fatalf("manager not reset: %+v", m)
	}
	m.mutex.Lock()
	if again := m.endSubscriptionLocked(old); again != nil {
		t.Fatal("an old subscription detached the manager twice")
	}
	m.mutex.Unlock()
}

// A viewer that joins (with a new codec, or the old one) while an ended
// subscription is still attached starts a new one: no conflict, no dead stream.
func TestAJoinOnAnEndedSubscriptionStartsOver(t *testing.T) {
	for _, codec := range []stream.VideoCodec{stream.VideoCodecH265, stream.VideoCodecH264} {
		old := &stream.VideoSubscription{}
		notified, started := fakeEndedSubscriptions(t, map[*stream.VideoSubscription]bool{old: true})
		m := NewWebRTCManager()
		_, stale := addViewer(t, m, old, stream.EncoderConfig{Codec: stream.VideoCodecH264})
		ws, joining := testConn(t), &Client{config: stream.EncoderConfig{Codec: codec}}
		if err := m.AddClient(ws, joining); err != nil {
			t.Fatalf("%s: join refused: %v", codec, err)
		}
		if len(*started) != 1 || m.subscription != (*started)[0] {
			t.Fatalf("%s: no new subscription", codec)
		}
		if len(m.clients) != 1 || m.clients[ws] != joining || m.config.Codec != codec {
			t.Fatalf("%s: clients %v config %v", codec, m.clients, m.config)
		}
		if len(*notified) != 1 || (*notified)[0] != stale {
			t.Fatalf("%s: the old viewer was not told: %v", codec, *notified)
		}
		if !subscriptionEnded(old) {
			t.Fatalf("%s: the ended subscription was not closed", codec)
		}
		m.mutex.Lock()
		for _, writer := range m.writers {
			writer.close()
		}
		m.mutex.Unlock()
	}
}

// A live subscription is still shared, and a different codec still conflicts.
func TestALiveSubscriptionIsUnchanged(t *testing.T) {
	live := &stream.VideoSubscription{}
	_, started := fakeEndedSubscriptions(t, map[*stream.VideoSubscription]bool{})
	m := NewWebRTCManager()
	h264 := stream.EncoderConfig{Codec: stream.VideoCodecH264}
	addViewer(t, m, live, h264)
	if err := m.AddClient(testConn(t), &Client{config: stream.EncoderConfig{Codec: stream.VideoCodecH265}}); err == nil {
		t.Fatal("a different codec joined a live subscription")
	}
	if err := m.AddClient(testConn(t), &Client{config: h264}); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 0 || m.subscription != live || len(m.clients) != 2 {
		t.Fatalf("a live subscription was replaced")
	}
	m.mutex.Lock()
	for _, writer := range m.writers {
		writer.close()
	}
	m.mutex.Unlock()
}

// The stream loops' end-of-subscription branch is the path a real encoder
// change takes; it needs a live subscription, so check its shape here.
func TestTheStreamLoopsDetachAnEndedSubscription(t *testing.T) {
	for _, file := range []string{"manager.go", "../direct/streamer.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		i := strings.Index(text, "if !ok {")
		if i < 0 {
			t.Fatalf("%s: no end-of-subscription branch", file)
		}
		branch := text[i : i+strings.Index(text[i:], "\n\t\t}\n")]
		if !strings.Contains(branch, "endSubscriptionLocked(subscription)") || strings.Contains(branch, "getClientsFor") ||
			strings.Contains(branch, "RemoveClient") || strings.Contains(branch, "removeClient") {
			t.Fatalf("%s: the ended branch does not detach at once:\n%s", file, branch)
		}
	}
}
