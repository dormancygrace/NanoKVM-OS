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
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const mjpegPartHeader = "--frame\r\nContent-Type: image/jpeg\r\n\r\n"

// Chromium commits the preceding image when the next part headers arrive.
// Advance them before flushing so dedup cannot delay a stopped screen.
// The next JPEG length is unknown, so parts are delimited by the boundary.
var mjpegNextPart = []byte("\r\n" + mjpegPartHeader)

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

// While an H.264 or H.265 stream has viewers, MJPEG runs at most at this
// rate, and lower next to a stream wider than 1920 (2560x1440): each JPEG
// costs the video stream about one frame (1080p90 H.264 alone 91 fps, next
// to MJPEG at 53 fps only 32 fps). Up to 1920 wide 30 cost nothing measurable
// against 27 (H.265 1080p90 80-85, 1080p60 57-60, 720p120 107-116); at
// 2560x1440 30 over 33 was worse than 25 over 27. The native library runs
// MJPEG's own VPSS output a little above these (33 and 27).
const (
	mjpegNextToVideoFPS     = 30
	mjpegNextToWideVideoFPS = 25
)

// mjpegRate is the MJPEG capture rate for a screen rate.
func mjpegRate(screenFPS int, videoActive, videoWide bool) int {
	if screenFPS <= 0 {
		return 1
	}
	limit := mjpegNextToVideoFPS
	if videoWide {
		limit = mjpegNextToWideVideoFPS
	}
	if videoActive && screenFPS > limit {
		return limit
	}
	return screenFPS
}

// mjpegVideo reports whether an H.264 or H.265 stream has viewers and whether
// it is wider than 1920 (the width the native library last encoded).
func mjpegVideo() (active, wide bool) {
	_, active = stream.ActiveEncoderConfig()
	return active, active && common.ReadVideoValue("/run/nanokvm/video_width") > 1920
}

func (s *Streamer) run() {
	common.CheckScreen()
	screen := common.GetCaptureScreen()
	videoActive, videoWide := mjpegVideo()
	fps := mjpegRate(screen.FPS, videoActive, videoWide)
	nextVideoCheck := time.Now().Add(time.Second)

	vision := common.GetKvmVision()

	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()
	delivery := frameDelivery{}
	var adaptive adaptiveQuality

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

		// The video state is checked once a second, not on every frame.
		if now := time.Now(); now.After(nextVideoCheck) || screen.FPS != 0 && screen.FPS < fps {
			nextVideoCheck = now.Add(time.Second)
			videoActive, videoWide = mjpegVideo()
			if rate := mjpegRate(screen.FPS, videoActive, videoWide); rate != fps && screen.FPS != 0 {
				fps = rate
				ticker.Reset(time.Second / time.Duration(fps))
			}
		}
		// Avoid captures nobody can use while browser writers are backed up.
		busy := !viewersReady(clients)
		quality := adaptive.next(int(screen.Quality), busy, time.Now())
		if busy {
			continue
		}

		// The source may have become 3840x2160 since the viewers connected.
		if mjpegBlocked() {
			stream.UpdateCaptureStatus(stream.CaptureModeMJPEG, common.MjpegBlockedResult)
			delivery.last = nil
			continue
		}

		data, result := vision.ReadMjpeg(screen.Width, screen.Height, uint16(quality))
		stream.UpdateCaptureStatus(stream.CaptureModeMJPEG, result)
		if result < 0 || result == 5 || len(data) == 0 {
			// Signal loss/recovery must force a fresh delivery, even when
			// the returning JPEG happens to match the last good one.
			delivery.last = nil
			continue
		}

		if delivery.offer(clients, data, time.Now()) {
			stream.GetMjpegFrameRateCounter().Update()
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

func writeFrame(c *gin.Context, controller *http.ResponseController, data []byte, first bool) (err error) {
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

	if first {
		if _, err = c.Writer.WriteString(mjpegPartHeader); err != nil {
			return err
		}
	}

	if _, err = c.Writer.Write(data); err != nil {
		return err
	}

	if _, err = c.Writer.Write(mjpegNextPart); err != nil {
		return err
	}

	if err = controller.Flush(); err != nil {
		return err
	}

	// Bound slow writes, not the idle wait for the next distinct image. An
	// armed HTTP/2 deadline resets the stream even when no write is pending.
	if err = controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}
