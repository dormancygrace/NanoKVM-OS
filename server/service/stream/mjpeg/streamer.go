package mjpeg

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/vm"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var crlf = []byte("\r\n")

const clientWriteTimeout = 5 * time.Second
const duplicateRefreshInterval = 5 * time.Second

// Inspired by IronKVM 14386101 (yuzi-co): suppress identical encoded JPEGs.
// Client generation is independent, so a new or lagging viewer still gets
// the current image even when every other viewer has already seen it.
type frameDelivery struct {
	last       []byte
	generation uint64
}

// SG2002 JPEG output starts with SOI and a two-byte APP9 sequence counter.
// The counter changes even when every decoded pixel and all coding tables are
// identical. Ignore only that exact leading marker's payload, preserving every
// other byte (including quantization/Huffman tables and compressed image data).
// Captured device frames and pixel comparison are documented in development notes.
func sameMjpegImage(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	prefix := []byte{0xff, 0xd8, 0xff, 0xe9, 0x00, 0x04}
	if len(a) > 8 && bytes.HasPrefix(a, prefix) && bytes.HasPrefix(b, prefix) {
		return bytes.Equal(a[8:], b[8:])
	}
	return bytes.Equal(a, b)
}

func (d *frameDelivery) offer(clients []*mjpegClient, data []byte, now time.Time) bool {
	if !sameMjpegImage(d.last, data) {
		d.last = data // ReadMjpeg returns an owned Go buffer, never mutated.
		d.generation++
	}
	sent := false
	for _, c := range clients {
		if c.ctx.Err() != nil {
			continue
		}
		if c.generation != d.generation || c.lastOffer.IsZero() || now.Sub(c.lastOffer) >= duplicateRefreshInterval {
			c.offer(data)
			c.generation = d.generation
			c.lastOffer = now
			sent = true
		}
	}
	return sent
}

func viewersReady(clients []*mjpegClient) bool {
	for _, c := range clients {
		if c.ctx.Err() == nil && len(c.frames) == 0 {
			return true
		}
	}
	return false
}

type Streamer struct {
	mutex          sync.Mutex
	clients        map[*mjpegClient]struct{}
	clientSnapshot atomic.Pointer[[]*mjpegClient]
	running        bool
	frameMutex     sync.RWMutex
	latestFrame    LatestFrame
	cacheRefs      int32
	viewerVersion  uint64
}

func NewStreamer() *Streamer {
	s := &Streamer{
		clients: make(map[*mjpegClient]struct{}),
	}
	s.updateClientSnapshotLocked()

	return s
}

type mjpegClient struct {
	ctx    context.Context
	frames chan []byte
	// Owned exclusively by the stream's capture loop.
	generation uint64
	lastOffer  time.Time
}

func newMjpegClient(ctx context.Context) *mjpegClient {
	return &mjpegClient{
		ctx:    ctx,
		frames: make(chan []byte, 1),
	}
}

func (c *mjpegClient) next() ([]byte, bool) {
	select {
	case <-c.ctx.Done():
		return nil, false
	default:
	}

	select {
	case data := <-c.frames:
		return data, true
	case <-c.ctx.Done():
		return nil, false
	}
}

func (c *mjpegClient) offer(data []byte) {
	select {
	case c.frames <- data:
		return
	default:
	}

	// Keep only the newest frame so a slow connection cannot stall capture.
	select {
	case <-c.frames:
	default:
	}
	select {
	case c.frames <- data:
	default:
	}
}

func (s *Streamer) AddClient(c *gin.Context) *mjpegClient {
	client := newMjpegClient(c.Request.Context())

	s.mutex.Lock()
	s.clients[client] = struct{}{}
	count := s.updateClientSnapshotLocked()
	s.viewerVersion++
	version := s.viewerVersion
	start := !s.running
	if start {
		s.running = true
	}
	s.mutex.Unlock()
	vm.UpdateHdmiViewerSnapshot("mjpeg", count, version)

	if start {
		go s.run()
		log.Debug("mjpeg stream started")
	}

	return client
}

func (s *Streamer) RemoveClient(client *mjpegClient) {
	s.mutex.Lock()
	if _, exists := s.clients[client]; !exists {
		s.mutex.Unlock()
		return
	}

	delete(s.clients, client)
	count := s.updateClientSnapshotLocked()
	s.viewerVersion++
	version := s.viewerVersion
	s.mutex.Unlock()
	vm.UpdateHdmiViewerSnapshot("mjpeg", count, version)

	log.Debugf("mjpeg connection removed, remaining clients: %d", count)
}

func (s *Streamer) updateClientSnapshotLocked() int {
	clients := make([]*mjpegClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clientSnapshot.Store(&clients)

	return len(clients)
}

func (s *Streamer) getClients() []*mjpegClient {
	clients := s.clientSnapshot.Load()
	if clients == nil {
		return nil
	}

	return *clients
}

func (s *Streamer) run() {
	common.CheckScreen()
	screen := common.GetCaptureScreen()
	fps := screen.FPS
	if fps == 0 {
		fps = 1
	}

	vision := common.GetKvmVision()

	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()
	delivery := frameDelivery{}

	for range ticker.C {
		screen = common.GetCaptureScreen()
		clients := s.getClients()
		if len(clients) == 0 {
			if s.stopIfIdle() {
				log.Debug("mjpeg stream stopped due to no clients")
				return
			}
			continue
		}

		if screen.FPS != fps && screen.FPS != 0 {
			fps = screen.FPS
			ticker.Reset(time.Second / time.Duration(fps))
		}
		// Snapshot consumers still need fresh captures even when browser
		// writers are backed up. Otherwise avoid captures nobody can use.
		if !s.frameCacheEnabled() && !viewersReady(clients) {
			continue
		}

		data, result := vision.ReadMjpeg(screen.Width, screen.Height, screen.Quality)
		stream.UpdateCaptureStatus(stream.CaptureModeMJPEG, result)
		if result < 0 || result == 5 || len(data) == 0 {
			// Signal loss/recovery must force a fresh delivery, even when
			// the returning JPEG happens to match the last good one.
			delivery.last = nil
			continue
		}

		if s.frameCacheEnabled() {
			s.setLatestFrame(data, screen.Width, screen.Height)
		}

		if delivery.offer(clients, data, time.Now()) {
			stream.GetFrameRateCounter().Update()
		}
	}
}

func (s *Streamer) stopIfIdle() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if len(s.clients) > 0 {
		return false
	}

	s.running = false
	return true
}

func (s *Streamer) setLatestFrame(data []byte, width uint16, height uint16) {
	frameCopy := append([]byte(nil), data...)

	s.frameMutex.Lock()
	defer s.frameMutex.Unlock()

	s.latestFrame = LatestFrame{
		Data:       frameCopy,
		Width:      width,
		Height:     height,
		CapturedAt: time.Now(),
	}
}

func (s *Streamer) clearLatestFrame() {
	s.frameMutex.Lock()
	defer s.frameMutex.Unlock()

	s.latestFrame = LatestFrame{}
}

func (s *Streamer) enableLatestFrameCache() {
	atomic.AddInt32(&s.cacheRefs, 1)
}

func (s *Streamer) disableLatestFrameCache() {
	for {
		current := atomic.LoadInt32(&s.cacheRefs)
		if current <= 0 {
			return
		}

		if atomic.CompareAndSwapInt32(&s.cacheRefs, current, current-1) {
			if current == 1 {
				s.clearLatestFrame()
			}
			return
		}
	}
}

func (s *Streamer) frameCacheEnabled() bool {
	return atomic.LoadInt32(&s.cacheRefs) > 0
}

func (s *Streamer) getLatestFrame() (LatestFrame, bool) {
	if !s.frameCacheEnabled() {
		return LatestFrame{}, false
	}

	s.frameMutex.RLock()
	defer s.frameMutex.RUnlock()

	if len(s.latestFrame.Data) == 0 {
		return LatestFrame{}, false
	}

	return LatestFrame{
		Data:       append([]byte(nil), s.latestFrame.Data...),
		Width:      s.latestFrame.Width,
		Height:     s.latestFrame.Height,
		CapturedAt: s.latestFrame.CapturedAt,
	}, true
}

func writeFrame(c *gin.Context, controller *http.ResponseController, data []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = c.Request.Context().Err()
			if err == nil {
				err = fmt.Errorf("panic recovered in writeFrame: %v", r)
			}
		}
	}()

	if err = controller.SetWriteDeadline(time.Now().Add(clientWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}

	header := "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: " + strconv.Itoa(len(data)) + "\r\n\r\n"
	if _, err = c.Writer.WriteString(header); err != nil {
		return err
	}

	if _, err = c.Writer.Write(data); err != nil {
		return err
	}

	if _, err = c.Writer.Write(crlf); err != nil {
		return err
	}

	return controller.Flush()
}
