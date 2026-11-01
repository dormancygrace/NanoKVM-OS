package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/remotemedia"
	"github.com/gorilla/websocket"
)

type remoteTestRequest struct {
	ID     uint32
	Offset uint64
	Length uint32
}

func remoteTestPattern(offset uint64, length int) []byte {
	data := make([]byte, length)
	for i := range data {
		x := offset + uint64(i)
		data[i] = byte((x * 17) ^ (x >> 7) ^ (x >> 15) ^ (x >> 21))
	}
	return data
}

func remoteTestPayload(q remoteTestRequest) []byte {
	data := append(make([]byte, 4), remoteTestPattern(q.Offset, int(q.Length))...)
	binary.BigEndian.PutUint32(data, q.ID)
	return data
}

func remoteTestWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not stop")
	}
}

func remoteTestFixture(t *testing.T, respond func(*websocket.Conn, remoteTestRequest, <-chan struct{}) error) (*remoteReader, *websocket.Conn) {
	t.Helper()
	ready := make(chan *remoteReader, 1)
	up := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		ws, err := up.Upgrade(w, q, nil)
		if err != nil {
			return
		}
		ws.SetReadLimit(remotemedia.MaxRead + 4)
		s := &remoteSession{ws: ws, closed: make(chan struct{}), size: 3*1024*1024 + 2048}
		ready <- newRemoteReader(s)
	}))
	t.Cleanup(server.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := <-ready
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		for {
			var q remoteTestRequest
			if ws.ReadJSON(&q) != nil {
				return
			}
			if q.Length == 0 || q.Length > remotemedia.MaxRead || q.Offset > r.session.size || uint64(q.Length) > r.session.size-q.Offset {
				t.Errorf("browser request outside image: %+v", q)
				ws.Close()
				return
			}
			var err error
			if respond == nil {
				err = ws.WriteMessage(websocket.BinaryMessage, remoteTestPayload(q))
			} else {
				err = respond(ws, q, r.session.closed)
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		r.close()
		ws.Close()
		remoteTestWait(t, peerDone)
	})
	return r, ws
}

func remoteTestRead(t *testing.T, r *remoteReader, offset uint64, length uint32) []byte {
	t.Helper()
	data, err := r.read(offset, length)
	if err != nil {
		t.Fatal(offset, length, err)
	}
	if !bytes.Equal(data, remoteTestPattern(offset, int(length))) {
		t.Fatalf("corruption at offset %d, length %d", offset, length)
	}
	return data
}

func TestRemoteReaderRanges(t *testing.T) {
	r, _ := remoteTestFixture(t, nil)
	ranges := [][2]uint64{{0, 65536}, {65536, 65536}, {131072, 65536},
		{200001, 1 << 20}, {200001, 1 << 20}, {123, 17}, {r.session.size - 4096, 4096}}
	for i := 0; i < 24; i++ {
		ranges = append(ranges, [2]uint64{uint64((i * 19283) % 1500000), uint64(1 + (i*14281)%(1<<20))})
	}
	// Establish a stream ending in the final partial prefetch block.
	for i := uint64(3); i > 0; i-- {
		ranges = append(ranges, [2]uint64{r.session.size - i*65536, 65536})
	}
	for _, v := range ranges {
		remoteTestRead(t, r, v[0], uint32(v[1]))
	}
	for _, v := range [][2]uint64{{0, 0}, {r.session.size, 1}, {^uint64(0), 2}, {0, remotemedia.MaxRead + 1}} {
		if _, err := r.read(v[0], uint32(v[1])); err == nil {
			t.Fatal("invalid range accepted", v)
		}
	}
}

func TestRemoteReaderRejectBadResponses(t *testing.T) {
	for _, bad := range []string{"id", "header", "short", "long", "text"} {
		for _, requestID := range []uint32{1, 3} {
			t.Run(fmt.Sprintf("%s/request%d", bad, requestID), func(t *testing.T) {
				r, _ := remoteTestFixture(t, func(ws *websocket.Conn, q remoteTestRequest, _ <-chan struct{}) error {
					data := remoteTestPayload(q)
					kind := websocket.BinaryMessage
					if q.ID == requestID {
						switch bad {
						case "id":
							data[3] ^= 1
						case "header":
							data = data[:2]
						case "short":
							data = data[:len(data)-1]
						case "long":
							data = append(data, 1)
						case "text":
							kind = websocket.TextMessage
						}
					}
					return ws.WriteMessage(kind, data)
				})
				for i := uint32(1); i < requestID; i++ {
					remoteTestRead(t, r, uint64(i-1)*65536, 65536)
				}
				if _, err := r.read(uint64(requestID-1)*65536, 65536); err == nil {
					t.Fatal("malformed response accepted")
				}
				remoteTestWait(t, r.session.closed)
			})
		}
	}
}

func TestRemoteReaderRejectUnsolicitedData(t *testing.T) {
	extra := make(chan struct{})
	r, _ := remoteTestFixture(t, func(ws *websocket.Conn, q remoteTestRequest, closed <-chan struct{}) error {
		if err := ws.WriteMessage(websocket.BinaryMessage, remoteTestPayload(q)); err != nil {
			return err
		}
		// Serialize both client writes, just as browser WebSocket.send does.
		select {
		case <-extra:
			return ws.WriteMessage(websocket.BinaryMessage, bytes.Repeat([]byte{0xa5}, 4100))
		case <-closed:
			return errors.New("test peer closed")
		}
	})
	got := remoteTestRead(t, r, 0, 4096)
	close(extra)
	remoteTestWait(t, r.session.closed)
	if !bytes.Equal(got, remoteTestPattern(0, 4096)) {
		t.Fatal("unsolicited data overwrote a borrowed reply")
	}
}

func TestRemoteReaderSocketCloseInterruptsPendingRead(t *testing.T) {
	for _, requestID := range []uint32{1, 3} {
		t.Run(fmt.Sprint(requestID), func(t *testing.T) {
			partial := make(chan struct{})
			r, _ := remoteTestFixture(t, func(ws *websocket.Conn, q remoteTestRequest, closed <-chan struct{}) error {
				data := remoteTestPayload(q)
				if q.ID != requestID {
					return ws.WriteMessage(websocket.BinaryMessage, data)
				}
				writer, err := ws.NextWriter(websocket.BinaryMessage)
				if err != nil {
					return err
				}
				defer writer.Close()
				_, err = writer.Write(data[:8192])
				close(partial)
				<-closed
				if err != nil {
					return err
				}
				return errors.New("test peer stopped during response")
			})
			for i := uint32(1); i < requestID; i++ {
				remoteTestRead(t, r, uint64(i-1)*65536, 65536)
			}
			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				if _, err := r.read(uint64(requestID-1)*65536, 65536); err == nil {
					t.Error("partial response succeeded")
				}
			}()
			remoteTestWait(t, partial)
			// This is also how the authentication watcher interrupts a session.
			r.session.ws.Close()
			remoteTestWait(t, readDone)
			remoteTestWait(t, r.session.closed)
		})
	}
}

func TestRemoteReaderSocketCloseRejectsCachedData(t *testing.T) {
	r, _ := remoteTestFixture(t, nil)
	for i := 0; i < 3; i++ {
		remoteTestRead(t, r, uint64(i*65536), 65536)
	}
	if r.pipe == nil {
		t.Fatal("expected active prefetch window")
	}
	r.session.ws.Close()
	remoteTestWait(t, r.session.closed)
	if _, err := r.read(2*65536, 4096); err == nil {
		t.Fatal("served cached payload after socket closure")
	}
}
