package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/remotemedia"
	"NanoKVM-Server/service/hid"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type remoteSession struct {
	kind      string
	cancel    context.CancelFunc
	name      string
	size      uint64
	ws        *websocket.Conn
	writes    sync.Mutex
	closed    chan struct{}
	closeOnce sync.Once
	readBytes atomic.Uint64
	mounted   bool // guarded by Service.mu
}

func (s *remoteSession) close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		if s.cancel != nil {
			s.cancel()
		}
		if s.ws != nil {
			s.ws.Close()
		}
	})
}
func (s *remoteSession) send(value any) error {
	s.writes.Lock()
	defer s.writes.Unlock()
	_ = s.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.ws.WriteJSON(value)
}
func (s *Service) RemoteStatus(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := gin.H{"connected": false, "source": s.remoteKind, "error": s.remoteError}
	if r := s.remote; r != nil {
		data = gin.H{"connected": true, "source": r.kind, "mounted": r.mounted, "name": r.name, "size": r.size, "readBytes": r.readBytes.Load()}
	}
	var rsp proto.Response
	rsp.OkRspWithData(c, data)
}
func (s *Service) DisconnectRemote(c *gin.Context) {
	s.mu.Lock()
	r := s.remote
	s.mu.Unlock()
	if r != nil {
		r.close()
	}
	var rsp proto.Response
	rsp.OkRsp(c)
}

func (s *Service) ConnectRemote(c *gin.Context) {
	upgrade := websocket.Upgrader{CheckOrigin: middleware.CheckWebSocketOrigin}
	ws, err := upgrade.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	r := &remoteSession{kind: "browser", ws: ws, closed: make(chan struct{})}
	defer r.close()
	stopWatch := middleware.WatchWebSocket(c.Request.Context(), ws)
	defer stopWatch()
	ws.SetReadLimit(remotemedia.MaxRead + 4)
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Size uint64 `json:"size"`
	}
	if err = ws.ReadJSON(&hello); err != nil {
		return
	}
	fail := func(err error) { _ = r.send(gin.H{"type": "error", "message": err.Error()}) }
	if hello.Type != "open" || !utf8.ValidString(hello.Name) || len(hello.Name) > 200 || strings.ContainsAny(hello.Name, "\x00\r\n") || !strings.EqualFold(filepath.Ext(hello.Name), ".iso") {
		fail(errors.New("Select an ISO file"))
		return
	}
	if err = remotemedia.ValidateImage(hello.Size); err != nil {
		fail(err)
		return
	}
	_, dvdErr := os.Stat(filepath.Join(filepath.Dir(mountDevice), "dvd"))
	if err = remotemedia.ValidateOptical(hello.Size, dvdErr == nil); err != nil {
		fail(err)
		return
	}
	r.name = filepath.Base(hello.Name)
	r.size = hello.Size
	if err := s.reserveRemote(r); err != nil {
		fail(err)
		return
	}
	defer s.releaseRemote(r, nil)
	// Keep one reader active even when the host is idle, so disconnect/revocation
	// immediately tears down the block device. The reader serializes browser requests.
	_ = ws.SetReadDeadline(time.Now().Add(45 * time.Second))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	reader := newRemoteReader(r)
	// The receiver and the auth watcher still stop browser-owned sessions while
	// idle. Heartbeats never run on device-owned HTTP image sessions.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.closed:
				return
			case <-c.Request.Context().Done():
				r.close()
				return
			case <-ticker.C:
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					r.close()
					return
				}
			}
		}
	}()
	_ = s.serveRemote(r, reader, func() error {
		return r.send(gin.H{"type": "mounted", "name": r.name, "size": r.size})
	})
}

// Reconnect only the controller already bound to this gadget. Keep HID file
// descriptors synchronized with the disconnect, matching local image mounts.
// Caller holds the HID lock across LUN insertion and re-enumeration.
func reconnectRemoteUSB() error {
	path := usbGadgetUDC
	controller, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(controller)) == "" {
		return errors.New("USB gadget is not connected")
	}
	h := hid.GetHid()
	h.CloseNoLock()
	defer h.OpenNoLock()
	if err = os.WriteFile(path, []byte("\n"), 0600); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return os.WriteFile(path, controller, 0600)
}
