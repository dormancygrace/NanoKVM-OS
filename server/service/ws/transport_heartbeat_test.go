package ws

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"NanoKVM-Server/service/inputcontrol"
	"github.com/gorilla/websocket"
)

func TestTransportHeartbeat(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		optIn, pong, survives bool
	}{
		{"paused page with held-input safety", true, true, true},
		{"unresponsive transport", true, false, false},
		{"legacy page retains application timeout", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const timeout = 200 * time.Millisecond
			ready := make(chan *Client, 1)
			serverDone := make(chan error, 1)
			upgrade := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrade.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				c := &Client{ws: conn, heartbeatTimeout: timeout}
				defer c.Close()
				ready <- c
				serverDone <- c.Read()
			}))
			defer server.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			c := <-ready
			var pings atomic.Int32
			conn.SetPingHandler(func(data string) error {
				pings.Add(1)
				if !tc.pong {
					return nil
				}
				return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
			})
			replies := make(chan struct{}, 2)
			readerDone := make(chan struct{})
			go func() {
				defer close(readerDone)
				for {
					_, data, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var message Message
					if json.Unmarshal(data, &message) == nil && message.Type == "heartbeat" {
						replies <- struct{}{}
					}
				}
			}()
			defer func() { _ = conn.Close(); <-readerDone }()
			if tc.optIn {
				if err := conn.WriteMessage(websocket.BinaryMessage, []byte{HoldKeepaliveEvent}); err != nil {
					t.Fatal(err)
				}
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, []byte{Heartbeat}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-replies:
			case <-time.After(time.Second):
				t.Fatal("initial application heartbeat not answered")
			}

			if !tc.survives {
				select {
				case err := <-serverDone:
					var timeoutErr net.Error
					if !errors.As(err, &timeoutErr) || !timeoutErr.Timeout() {
						t.Fatalf("read error = %v, want timeout", err)
					}
				case <-time.After(time.Second):
					t.Fatal("silent application/transport never timed out")
				}
				if pings.Load() == 0 {
					t.Fatal("server never probed the transport")
				}
				return
			}

			// Pong proves only transport liveness. It must not renew a held
			// key while the page's dedicated hold keepalives are paused.
			c.inputMutex.Lock()
			c.noteHeldInputLocked(inputcontrol.ManualKeyboard, true)
			c.inputMutex.Unlock()
			select {
			case err := <-serverDone:
				t.Fatalf("live paused page disconnected: %v", err)
			case <-time.After(3 * timeout):
			}
			if pings.Load() < 3 {
				t.Fatalf("only %d transport probes", pings.Load())
			}
			c.inputMutex.Lock()
			held, timer := c.hold.held, c.hold.timer
			c.inputMutex.Unlock()
			if held != [3]bool{} || timer != nil {
				t.Fatal("transport Pong renewed held input")
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, []byte{Heartbeat}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-replies:
			case <-time.After(time.Second):
				t.Fatal("application did not resume on the original connection")
			}
			_ = conn.Close()
			select {
			case <-serverDone:
			case <-time.After(time.Second):
				t.Fatal("read/heartbeat worker did not stop after close")
			}
		})
	}
}
