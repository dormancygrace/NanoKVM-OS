package rustdesk

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"NanoKVM-Server/common"
	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/inputcontrol"
	"NanoKVM-Server/service/picoclaw"
	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/stream/audio"
	"NanoKVM-Server/service/ws"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const RuntimeDir = "/run/nanokvm-rustdesk"
const Binary = "/usr/bin/nanokvm-rustdesk"

// Bridge has no network listener. Socket mode and SO_PEERCRED restrict access
// to the local root-owned add-on service.
type Bridge struct {
	mu               sync.Mutex
	media            net.Listener
	audio            net.Listener
	control          *http.Server
	rtc              *rtcBridge
	sessions         map[string]*hidSession
	videoConnections map[net.Conn]struct{}
	audioConnections map[net.Conn]struct{}
	audioEnabled     func() bool
	audioSubscribe   func() (audioSource, error)
	prepareUSB       func() error
}

func NewBridge() *Bridge {
	return &Bridge{sessions: make(map[string]*hidSession), videoConnections: make(map[net.Conn]struct{}), audioConnections: make(map[net.Conn]struct{}), audioEnabled: audio.Enabled, audioSubscribe: func() (audioSource, error) { return audio.Subscribe() }}
}

func rootPeer(conn net.Conn) bool {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false
	}
	var credential *unix.Ucred
	if raw.Control(func(fd uintptr) { credential, err = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }) != nil {
		return false
	}
	return err == nil && credential != nil && credential.Uid == 0
}

type privateListener struct{ net.Listener }

func (l privateListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if rootPeer(c) {
			return c, nil
		}
		c.Close()
	}
}

func listenSocket(path string) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s is not a socket", path)
		}
		c, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			c.Close()
			return nil, fmt.Errorf("%s is already serving", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return privateListener{l}, nil
}

func (b *Bridge) Start() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.media != nil {
		return nil
	}
	if err := os.MkdirAll(RuntimeDir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(RuntimeDir, 0700); err != nil {
		return err
	}
	media, err := listenSocket(filepath.Join(RuntimeDir, "media.sock"))
	if err != nil {
		return err
	}
	control, err := listenSocket(filepath.Join(RuntimeDir, "control.sock"))
	if err != nil {
		media.Close()
		return err
	}
	rtc, err := listenSocket(filepath.Join(RuntimeDir, "webrtc.sock"))
	if err != nil {
		media.Close()
		control.Close()
		return err
	}
	audioListener, err := listenSocket(filepath.Join(RuntimeDir, "audio.sock"))
	if err != nil {
		media.Close()
		control.Close()
		rtc.Close()
		return err
	}
	b.rtc = startRTCBridge(rtc)
	b.audio = audioListener
	go b.acceptAudio(audioListener)
	b.media = media
	b.control = &http.Server{Handler: http.HandlerFunc(b.serveHID), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 55 * time.Second, MaxHeaderBytes: 4096}
	go b.control.Serve(control)
	go func() {
		for {
			c, err := media.Accept()
			if err != nil {
				return
			}
			b.mu.Lock()
			if b.media != media || len(b.videoConnections) >= 8 {
				b.mu.Unlock()
				c.Close()
				continue
			}
			b.videoConnections[c] = struct{}{}
			b.mu.Unlock()
			go func() {
				defer func() { c.Close(); b.mu.Lock(); delete(b.videoConnections, c); b.mu.Unlock() }()
				b.serveMedia(c)
			}()
		}
	}()
	return nil
}

func (b *Bridge) Stop() {
	b.mu.Lock()
	media, control, rtc, audioListener := b.media, b.control, b.rtc, b.audio
	b.media, b.control, b.rtc, b.audio = nil, nil, nil, nil
	sessions := make([]*hidSession, 0, len(b.sessions))
	for _, s := range b.sessions {
		sessions = append(sessions, s)
	}
	b.sessions = make(map[string]*hidSession)
	for c := range b.videoConnections {
		c.Close()
	}
	for c := range b.audioConnections {
		c.Close()
	}
	b.mu.Unlock()
	if media != nil {
		media.Close()
	}
	if control != nil {
		control.Close()
	}
	if rtc != nil {
		rtc.close()
	}
	if audioListener != nil {
		audioListener.Close()
	}
	for _, s := range sessions {
		s.close()
	}
}

// Monitor supports terminal apk add/del without restarting the KVM app.
func (b *Bridge) Monitor(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(Binary); err == nil {
			_ = b.Start()
		} else if errors.Is(err, os.ErrNotExist) {
			b.Stop()
		}
		b.expire()
		select {
		case <-ctx.Done():
			b.Stop()
			return
		case <-ticker.C:
		}
	}
}

func frameHeader(codec stream.VideoCodec, width, height uint16, sequence uint64, frame stream.VideoFrame) []byte {
	h := make([]byte, 40)
	copy(h, "OKVF")
	h[4] = 1
	h[5] = byte(codec.NativeCodec())
	if frame.IsKeyframe() {
		h[6] = 1
	}
	binary.BigEndian.PutUint64(h[8:16], sequence)
	binary.BigEndian.PutUint64(h[16:24], uint64(frame.Timestamp))
	binary.BigEndian.PutUint32(h[24:28], uint32(frame.Duration.Microseconds()))
	binary.BigEndian.PutUint16(h[28:30], width)
	binary.BigEndian.PutUint16(h[30:32], height)
	binary.BigEndian.PutUint32(h[32:36], uint32(len(frame.Data)))
	return h
}

// Codec zero denotes a bounded UTF-8 error instead of encoded frame data.
func writeMediaError(c net.Conn, message string) {
	data := []byte(message)
	if len(data) > 2048 {
		data = data[:2048]
	}
	header := make([]byte, 40)
	copy(header, "OKVF")
	header[4] = 1
	binary.BigEndian.PutUint32(header[32:36], uint32(len(data)))
	c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	c.Write(append(header, data...))
}

func (b *Bridge) serveMedia(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReaderSize(c, 4096)
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > 2048 {
		return
	}
	var req struct {
		Version int
		Video   string
		Codec   stream.VideoCodec
	}
	if json.Unmarshal(line, &req) != nil || req.Version != 1 || (req.Video != "info" && req.Video != "encoded") || (req.Codec != "" && req.Codec != "auto" && req.Codec != stream.VideoCodecH264 && req.Codec != stream.VideoCodecH265) {
		return
	}
	var subscription *stream.VideoSubscription
	if req.Codec == "" || req.Codec == "auto" {
		subscription, err = stream.SubscribeCurrentVideo()
	} else {
		subscription, err = stream.SubscribeVideo(stream.EncoderConfig{Codec: req.Codec})
	}
	if err != nil {
		writeMediaError(c, err.Error())
		return
	}
	defer subscription.Close()
	codec := subscription.Config().Codec
	c.SetReadDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer subscription.Close()
		for {
			line, err := reader.ReadSlice('\n')
			if err != nil || len(line) > 256 {
				return
			}
			if string(line) == "{\"request\":\"keyframe\"}\n" {
				stream.RequestKeyframe()
			}
		}
	}()
	startup, cancelStartup := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStartup()
	ready := false
	for seq := uint64(1); ; seq++ {
		var frame stream.VideoFrame
		var ok bool
		if ready {
			frame, ok = subscription.Next()
		} else {
			frame, ok = waitInitialMediaFrame(startup, subscription.NextContext)
		}
		if !ok || frame.Result < 0 || len(frame.Data) > 8<<20 {
			writeMediaError(c, "HDMI encoder is unavailable")
			return
		}
		width, height := mediaDimensions()
		if width == 0 || height == 0 {
			if !ready && startup.Err() == nil {
				stream.RequestKeyframe()
				continue
			}
			writeMediaError(c, "HDMI dimensions are unavailable")
			return
		}
		ready = true
		cancelStartup()
		data := frame.Data
		if req.Video == "info" {
			frame.Data = nil
		}
		c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Write(frameHeader(codec, width, height, seq, frame)); err != nil {
			return
		}
		if req.Video == "info" {
			return
		}
		if _, err := c.Write(data); err != nil {
			return
		}
		select {
		case <-done:
			return
		default:
		}
	}
}

type hidSession struct {
	id              string
	mu              sync.Mutex
	closed          atomic.Bool
	touched         time.Time
	manual          *inputcontrol.ManualSession
	relativeOnly    bool
	relativePointer relativePointer
	writeHID        func(inputcontrol.ManualReportKind, []byte) error
	held            map[inputcontrol.ManualReportKind][]byte
}

func writeUSBReport(kind inputcontrol.ManualReportKind, report []byte) error {
	device := hid.GetHid()
	switch kind {
	case inputcontrol.ManualKeyboard:
		return device.WriteKeyboardReport(report)
	case inputcontrol.ManualRelativeMouse:
		return device.WriteRelativeMouseReport(report)
	case inputcontrol.ManualAbsoluteMouse:
		return device.WriteAbsoluteMouseReport(report)
	default:
		return errors.New("invalid HID report kind")
	}
}

func releaseReport(kind inputcontrol.ManualReportKind, previous []byte) []byte {
	report := make([]byte, len(previous))
	if kind == inputcontrol.ManualAbsoluteMouse && len(previous) >= 5 {
		copy(report[1:5], previous[1:5])
	}
	return report
}

func reportHeld(kind inputcontrol.ManualReportKind, report []byte) bool {
	if report[0] != 0 {
		return true
	}
	if kind == inputcontrol.ManualKeyboard {
		for _, key := range report[2:] {
			if key != 0 {
				return true
			}
		}
	}
	return false
}

// Caller holds both the session mutex and the manual input write lock.
func (s *hidSession) releaseKind(kind inputcontrol.ManualReportKind) error {
	previous := s.held[kind]
	if previous == nil {
		return nil
	}
	if err := s.writeHID(kind, releaseReport(kind, previous)); err != nil {
		return err
	}
	delete(s.held, kind)
	s.manual.Reset(kind)
	return nil
}

func (s *hidSession) releaseHeld() error {
	var errs []error
	for _, kind := range []inputcontrol.ManualReportKind{inputcontrol.ManualKeyboard, inputcontrol.ManualRelativeMouse, inputcontrol.ManualAbsoluteMouse} {
		if err := s.releaseKind(kind); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *hidSession) writeReport(kind inputcontrol.ManualReportKind, report []byte) error {
	if kind == inputcontrol.ManualRelativeMouse {
		if err := s.releaseKind(inputcontrol.ManualAbsoluteMouse); err != nil {
			return err
		}
	} else if kind == inputcontrol.ManualAbsoluteMouse {
		if err := s.releaseKind(inputcontrol.ManualRelativeMouse); err != nil {
			return err
		}
	}
	if reportHeld(kind, report) {
		if s.held == nil {
			s.held = make(map[inputcontrol.ManualReportKind][]byte)
		}
		// A failed write may have partially reached USB. Retain its release report.
		s.held[kind] = append([]byte(nil), report...)
	}
	if err := s.writeHID(kind, report); err != nil {
		return err
	}
	if !reportHeld(kind, report) {
		delete(s.held, kind)
	}
	return nil
}

func (s *hidSession) close() {
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return
	}
	s.closed.Store(true)
	if err := s.manual.Execute(s.releaseHeld); err != nil {
		log.Errorf("RustDesk HID release failed: %s", err)
	}
	s.mu.Unlock()
	s.manual.Close()
	ws.GetManager().ReleaseExternalInput(s.id)
}

func (b *Bridge) session(id string, create bool) (*hidSession, error) {
	if len(id) != 32 {
		return nil, errors.New("invalid session")
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return nil, errors.New("invalid session")
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.sessions[id]; s != nil {
		return s, nil
	}
	if !create {
		return nil, nil
	}
	if len(b.sessions) >= 8 {
		return nil, errors.New("too many HID sessions")
	}
	s := &hidSession{id: id, touched: time.Now(), writeHID: writeUSBReport, manual: inputcontrol.NewManualSession(nil, nil)}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		return nil, errors.New("another session holds input control")
	}
	device := hid.GetHid()
	device.Open()
	s.relativeOnly = device.RelativeMouseOnly()
	if s.relativeOnly {
		width, height := mediaDimensions()
		s.relativePointer.width, s.relativePointer.height = int32(width), int32(height)
	}
	b.sessions[id] = s
	return s, nil
}

func (b *Bridge) expire() {
	b.mu.Lock()
	var expired []*hidSession
	for id, s := range b.sessions {
		s.mu.Lock()
		stale := s.closed.Load() || time.Since(s.touched) > 10*time.Second
		s.mu.Unlock()
		if stale {
			delete(b.sessions, id)
			expired = append(expired, s)
		}
	}
	b.mu.Unlock()
	for _, s := range expired {
		s.close()
	}
}

func (b *Bridge) serveHID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	path := r.URL.Path
	if path == "/api/hid/prepare" {
		b.mu.Lock()
		prepare := b.prepareUSB
		b.mu.Unlock()
		if prepare == nil {
			http.Error(w, "USB preparation is unavailable", 503)
			return
		}
		if err := prepare(); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.WriteHeader(204)
		return
	}
	heartbeat, closeSession := path == "/api/hid/heartbeat", path == "/api/hid/close"
	if !heartbeat && !closeSession && path != "/api/hid/keyboard" && path != "/api/hid/mouse" && path != "/api/hid/mouse/absolute" {
		http.NotFound(w, r)
		return
	}
	id := r.Header.Get("X-NanoKVM-Session")
	s, err := b.session(id, !heartbeat && !closeSession)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if closeSession {
		if s != nil {
			s.close()
			b.mu.Lock()
			delete(b.sessions, id)
			b.mu.Unlock()
		}
		w.WriteHeader(204)
		return
	}
	if s == nil {
		w.WriteHeader(204)
		return
	}
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		http.Error(w, "session was preempted", 409)
		return
	}
	s.touched = time.Now()
	if heartbeat {
		s.mu.Unlock()
		w.WriteHeader(204)
		return
	}
	defer s.mu.Unlock()
	var body struct {
		Modifiers uint8
		Keys      []uint8
		Buttons   uint8
		X         int32
		Y         int32
		Wheel     int8
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid report", 400)
		return
	}
	var report []byte
	var extraReports [][]byte
	kind := inputcontrol.ManualRelativeMouse
	if path == "/api/hid/keyboard" {
		if len(body.Keys) > 6 {
			http.Error(w, "keyboard rollover", 400)
			return
		}
		report = make([]byte, 8)
		report[0] = body.Modifiers
		copy(report[2:], body.Keys)
		kind = inputcontrol.ManualKeyboard
	} else if path == "/api/hid/mouse/absolute" {
		if body.X < 0 || body.X > 32767 || body.Y < 0 || body.Y > 32767 {
			http.Error(w, "invalid coordinates", 400)
			return
		}
		report = make([]byte, 7)
		report[0] = body.Buttons
		binary.LittleEndian.PutUint16(report[1:3], uint16(body.X))
		binary.LittleEndian.PutUint16(report[3:5], uint16(body.Y))
		report[5] = byte(body.Wheel)
		kind = inputcontrol.ManualAbsoluteMouse
		if s.relativeOnly {
			reports := s.relativePointer.reports(body.Buttons, body.X, body.Y, body.Wheel)
			report, extraReports = reports[0], reports[1:]
			kind = inputcontrol.ManualRelativeMouse
		}
	} else {
		if body.X < -127 || body.X > 127 || body.Y < -127 || body.Y > 127 {
			http.Error(w, "invalid movement", 400)
			return
		}
		report = []byte{body.Buttons, byte(int8(body.X)), byte(int8(body.Y)), byte(body.Wheel), 0}
	}
	held := reportHeld(kind, report)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	allow := func(mode controlmode.Mode) bool {
		return ws.GetManager().AllowsInputLease(id) && (mode != controlmode.ModePicoclaw || !picoclaw.GetSessionLock().BlocksManualInput())
	}
	reservation, err := s.manual.Reserve(ctx, kind, held, allow)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	// The RustDesk input task already serializes events. Execute USB writes in
	// this request instead of adding another scheduling hop through HID workers.
	started := false
	err = reservation.Execute(func() error {
		for _, next := range append([][]byte{report}, extraReports...) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.closed.Load() || !ws.GetManager().AllowsInputLease(id) {
				return inputcontrol.ErrManualInputBlocked
			}
			started = true
			if err := s.writeReport(kind, next); err != nil {
				if cleanupErr := s.releaseHeld(); cleanupErr != nil {
					log.Errorf("RustDesk HID write cleanup failed: %s", cleanupErr)
				}
				return err
			}
		}
		return nil
	})
	reservation.Complete(err == nil)
	if err != nil {
		log.Errorf("RustDesk HID request failed: path=%s started=%t error=%s", path, started, err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "HID request timed out", 504)
		} else if errors.Is(err, inputcontrol.ErrManualInputBlocked) {
			http.Error(w, "input control was revoked", 409)
		} else {
			http.Error(w, "USB HID write failed", 503)
		}
		return
	}

	w.WriteHeader(204)
}

// Runtime dimensions must fit the RustDesk frame header without wrapping.
// Missing or invalid values retain the existing zero/fallback behavior.
func readMediaDimension(path string) uint16 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 16)
	if err != nil {
		return 0
	}
	return uint16(value)
}

func mediaDimensions() (uint16, uint16) {
	screen := common.GetCaptureScreen()
	width, height := screen.Width, screen.Height
	if w := readMediaDimension("/run/nanokvm/stream_width"); w > 0 {
		width = w
	}
	if h := readMediaDimension("/run/nanokvm/stream_height"); h > 0 {
		height = h
	}
	if width == 0 || height == 0 {
		width = readMediaDimension("/run/nanokvm/width")
		height = readMediaDimension("/run/nanokvm/height")
	}
	return width, height
}

// Cold encoder startup can produce temporary errors before its first frame.
// A persistent capture failure still expires within the login metadata budget.
func waitInitialMediaFrame(ctx context.Context, next func(context.Context) (stream.VideoFrame, bool)) (stream.VideoFrame, bool) {
	for ctx.Err() == nil {
		frame, ok := next(ctx)
		if !ok || frame.Result >= 0 {
			return frame, ok
		}
	}
	return stream.VideoFrame{}, false
}
