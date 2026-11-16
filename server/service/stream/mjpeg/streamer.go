package mjpeg

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/internal/videogate"
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

// framed, when not nil, is data behind room for a whole-picture message
// header: tiled viewers then get it without a copy of the picture.
func (d *frameDelivery) offer(clients []*mjpegClient, data, framed []byte, now time.Time) bool {
	if !sameMjpegImage(d.last, data) {
		d.last = data // ReadMjpeg returns an owned Go buffer, never mutated.
		d.generation++
	}
	sent := false
	var whole []byte // for tiled viewers, made once
	for _, c := range clients {
		if c.ctx.Err() != nil {
			continue
		}
		if c.generation != d.generation || c.lastOffer.IsZero() || now.Sub(c.lastOffer) >= duplicateRefreshInterval {
			if c.tiled {
				if whole == nil && framed != nil {
					whole = common.MjpegTilesWholeInPlace(framed)
				} else if whole == nil {
					whole = common.MjpegTilesWholeMessage(data)
				}
				if whole == nil {
					continue
				}
				c.offerTile(whole, true)
			} else {
				c.offer(data)
			}
			c.generation = d.generation
			c.lastOffer = now
			sent = true
		}
	}
	return sent
}

// viewersDue reports whether a viewer will want a picture by the time one is
// read: nothing queued, and its current write ends within lead. A picture read
// earlier would wait behind the write and be replaced by the next one, read
// and encoded for nothing; one read later would be older when it is sent.
func viewersDue(clients []*mjpegClient, now time.Time, lead time.Duration) bool {
	for _, c := range clients {
		if c.ctx.Err() == nil && len(c.frames) == 0 && c.writeEndsWithin(now, lead) {
			return true
		}
	}
	return false
}

// captureTick decides one capture tick: read a picture now, and whether the
// tick counts as busy for the adaptive quality. A tick is busy when no viewer
// will want a picture within lead: each has one queued, or its writer is
// still busy with an earlier one. On a slow link that is where the pacing
// holds reads back, and the quality must step down as it did when such
// pictures were read and queued. An idle writer (a still screen, where
// nothing new is sent) never makes a tick busy, so the quality climbs back.
func captureTick(clients []*mjpegClient, now time.Time, lead time.Duration) (busy, read bool) {
	read = viewersDue(clients, now, lead)
	return !read, read
}

// viewersReady reports whether a viewer can take the next picture. A tiled
// viewer may have one message waiting behind the one being written, since its
// messages cannot be skipped; more would only make the picture lag.
func viewersReady(clients []*mjpegClient) bool {
	for _, c := range clients {
		if c.ctx.Err() == nil && (len(c.frames) == 0 || c.tiled && len(c.frames) < 2) {
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
	// tiled: the viewer takes tile messages (tiles.go).
	tiled bool
	// Owned exclusively by the stream's capture loop.
	generation uint64
	lastOffer  time.Time
	// A tiled viewer waiting for a whole picture.
	resync bool
	// Set by the viewer's writer: when its current write started (Unix ns,
	// 0 while it waits), and how long its writes usually take (ns).
	writeStart, writeTime atomic.Int64
}

// writing and wrote bracket one write to the viewer.
func (c *mjpegClient) writing(now time.Time) { c.writeStart.Store(now.UnixNano()) }

func (c *mjpegClient) wrote(now time.Time) {
	took := now.UnixNano() - c.writeStart.Swap(0)
	if usual := c.writeTime.Load(); usual != 0 {
		took = usual + (took-usual)/4
	}
	c.writeTime.Store(took)
}

// writeEndsWithin reports whether the viewer's current write is due to end
// within lead, by its usual write time, or it is not writing.
func (c *mjpegClient) writeEndsWithin(now time.Time, lead time.Duration) bool {
	start := c.writeStart.Load()
	return start == 0 || start+c.writeTime.Load()-now.UnixNano() <= int64(lead)
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
	return s.addClient(newMjpegClient(c.Request.Context()))
}

func (s *Streamer) AddTiledClient(ctx context.Context) *mjpegClient {
	return s.addClient(newTiledClient(ctx))
}

func (s *Streamer) addClient(client *mjpegClient) *mjpegClient {

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

// Limit the shared MJPEG capture while H.264/H.265 has viewers. Its own
// VPSS output runs slightly faster so JPEG reads need not wait for a frame.
// UHD inputs are refused separately by the MJPEG input-size guard.
const mjpegNextToVideoFPS = 30

// mjpegRate is the MJPEG capture rate for a screen rate.
func mjpegRate(screenFPS int, videoActive bool) int {
	if screenFPS <= 0 {
		return 1
	}
	if videoActive && screenFPS > mjpegNextToVideoFPS {
		return mjpegNextToVideoFPS
	}
	return screenFPS
}

func (s *Streamer) run() {
	// Background OCR has ended before the JPEG encoder takes its memory (the
	// viewer snapshot that woke the capture held the gate already).
	release, _ := videogate.Hold(nil)
	defer release()
	common.CheckScreen()
	screen := common.GetCaptureScreen()
	_, videoActive := stream.ActiveEncoderConfig()
	fps := mjpegRate(screen.FPS, videoActive)
	nextVideoCheck := time.Now().Add(time.Second)

	vision := common.GetKvmVision()

	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()
	delivery := frameDelivery{}
	tiles := tileDelivery{wantWhole: true}
	var adaptive adaptiveQuality
	// How long a read usually takes: a read starts that long, plus a tick,
	// before the viewer's write is due to end.
	var readTime time.Duration

	for range ticker.C {
		screen = common.GetCaptureScreen()
		clients := s.getClients()
		if len(clients) == 0 {
			if s.stopIfIdle() {
				tiles.release()
				log.Debug("mjpeg stream stopped due to no clients")
				return
			}
			continue
		}

		// The video state is checked once a second, not on every frame.
		if now := time.Now(); now.After(nextVideoCheck) || screen.FPS != 0 && screen.FPS < fps {
			nextVideoCheck = now.Add(time.Second)
			_, videoActive = stream.ActiveEncoderConfig()
			if rate := mjpegRate(screen.FPS, videoActive); rate != fps && screen.FPS != 0 {
				fps = rate
				ticker.Reset(time.Second / time.Duration(fps))
			}
		}
		// Avoid captures nobody can use while browser writers are backed up.
		readStart := time.Now()
		busy, read := captureTick(clients, readStart, time.Second/time.Duration(fps)+readTime)
		quality := adaptive.next(int(screen.Quality), busy, readStart)
		if !read {
			continue
		}

		// The source may have become 3840x2160 since the viewers connected:
		// native capture refuses it, and the refusal is held for a moment.
		if mjpegHeld() {
			logBlocked()
			stream.UpdateCaptureStatus(stream.CaptureModeMJPEG, common.MjpegBlockedResult)
			delivery.last = nil
			tiles.wantWhole = true
			continue
		}

		if tilesOnly(clients) {
			sent := tiles.read(clients, &delivery, screen, quality, time.Now())
			readTime += (time.Since(readStart) - readTime) / 4
			if sent {
				stream.GetMjpegFrameRateCounter().Update()
			}
			continue
		}
		tiles.wantWhole = true

		data, result := vision.ReadMjpeg(screen.Width, screen.Height, uint16(quality))
		readTime += (time.Since(readStart) - readTime) / 4
		stream.UpdateCaptureStatus(stream.CaptureModeMJPEG, result)
		if result == common.MjpegBlockedResult {
			logBlocked()
		} else if result >= 0 {
			blockedLogged.Store(false)
		}
		if result < 0 || result == 5 || len(data) == 0 {
			// Signal loss/recovery must force a fresh delivery, even when
			// the returning JPEG happens to match the last good one.
			delivery.last = nil
			continue
		}

		if delivery.offer(clients, data, nil, time.Now()) {
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
