package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
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
	name      string
	size      uint64
	ws        *websocket.Conn
	writes    sync.Mutex
	responses chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	readID    uint32
	readBytes atomic.Uint64
	mounted   bool // guarded by Service.mu
}

func (s *remoteSession) close() { s.closeOnce.Do(func() { close(s.closed); s.ws.Close() }) }
func (s *remoteSession) send(value any) error {
	s.writes.Lock()
	defer s.writes.Unlock()
	_ = s.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.ws.WriteJSON(value)
}
func (s *remoteSession) read(offset uint64, length uint32) ([]byte, error) {
	s.readID++
	if err := s.send(gin.H{"type": "read", "id": s.readID, "offset": offset, "length": length}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-s.closed:
		return nil, errors.New("browser disconnected")
	case <-timer.C:
		s.close()
		return nil, errors.New("browser read timed out")
	case data := <-s.responses:
		if len(data) != int(length)+4 || binary.BigEndian.Uint32(data[:4]) != s.readID {
			s.close()
			return nil, errors.New("invalid browser block response")
		}
		s.readBytes.Add(uint64(length))
		return data[4:], nil
	}
}

func (s *Service) RemoteStatus(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := gin.H{"connected": false}
	if r := s.remote; r != nil {
		data = gin.H{"connected": true, "mounted": r.mounted, "name": r.name, "size": r.size, "readBytes": r.readBytes.Load()}
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
	r := &remoteSession{ws: ws, responses: make(chan []byte, 1), closed: make(chan struct{})}
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
	s.mu.Lock()
	if s.remote != nil {
		s.mu.Unlock()
		fail(errors.New("Browser media is already connected"))
		return
	}
	current, err := os.ReadFile(mountDevice)
	if err != nil {
		s.mu.Unlock()
		fail(errors.New("Enable USB mass storage in USB Composition first"))
		return
	}
	if strings.TrimSpace(string(current)) != "" {
		s.mu.Unlock()
		fail(errors.New("Unmount the current image first"))
		return
	}
	s.remote = r
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.remote == r {
			s.remote = nil
		}
		s.mu.Unlock()
	}()
	// Keep one reader active even when the host is idle, so disconnect/revocation
	// immediately tears down the block device. Only the NBD worker requests data.
	_ = ws.SetReadDeadline(time.Now().Add(45 * time.Second))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	go func() {
		defer r.close()
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if kind != websocket.BinaryMessage || len(data) < 4 {
				return
			}
			select {
			case r.responses <- data:
			case <-r.closed:
				return
			default:
				return
			}
		}
	}()
	device, err := remotemedia.Open(r.size, r.read)
	if err != nil {
		fail(err)
		return
	}
	defer func() {
		r.close()
		device.Close() // unblock gadget reads before removing the medium
		s.mu.Lock()
		defer s.mu.Unlock()
		if data, _ := os.ReadFile(mountDevice); strings.TrimSpace(string(data)) == remotemedia.DevicePath {
			if err := os.WriteFile(mountDevice, []byte("\n"), 0600); err != nil {
				// Host prevention of medium removal must not retain disconnected media.
				_ = os.WriteFile(filepath.Join(filepath.Dir(mountDevice), "forced_eject"), []byte("1"), 0600)
			}
		}
	}()
	// Re-enumerate after insertion, as the existing SD-image mount does.
	// Hosts cache the SCSI device type, so a CD-ROM/drive switch needs this.
	s.mu.Lock()
	for _, item := range []struct{ path, value string }{{roFlag, "1"}, {cdromFlag, "1"}, {inquiryString, fmt.Sprintf("%-8s%-16s%04x", "NanoKVM", "Remote ISO", 0x0520)}, {mountDevice, remotemedia.DevicePath}} {
		if err = os.WriteFile(item.path, []byte(item.value), 0600); err != nil {
			break
		}
	}
	if err == nil {
		err = reconnectRemoteUSB()
	}
	if err == nil {
		r.mounted = true
	}
	s.mu.Unlock()
	if err != nil {
		fail(fmt.Errorf("Connect remote ISO: %w", err))
		return
	}
	if err = r.send(gin.H{"type": "mounted", "name": r.name, "size": r.size}); err != nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.closed:
			return
		case <-device.Stopped():
			fail(errors.New("Remote media device disconnected"))
			return
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			if err = ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

// Reconnect only the controller already bound to this gadget. Keep HID file
// descriptors synchronized with the disconnect, matching local image mounts.
func reconnectRemoteUSB() error {
	const path = "/sys/kernel/config/usb_gadget/g0/UDC"
	controller, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(controller)) == "" {
		return errors.New("USB gadget is not connected")
	}
	h := hid.GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() { h.OpenNoLock(); h.Unlock() }()
	if err = os.WriteFile(path, []byte("\n"), 0600); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return os.WriteFile(path, controller, 0600)
}
