package rustdesk

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/inputcontrol"
	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/ws"
)

func TestMediaHeaderAndDiagnosticFrame(t *testing.T) {
	frame := stream.VideoFrame{Data: []byte{1, 2, 3}, Result: 3, Timestamp: 100_000, Duration: 20 * time.Millisecond}
	h := frameHeader(stream.VideoCodecH265, 1440, 2560, 7, frame)
	if len(h) != 40 || string(h[:4]) != "OKVF" || h[4] != 1 || h[5] != 2 || h[6] != 1 ||
		binary.BigEndian.Uint64(h[8:16]) != 7 || binary.BigEndian.Uint64(h[16:24]) != 100_000 ||
		binary.BigEndian.Uint32(h[24:28]) != 20_000 || binary.BigEndian.Uint16(h[28:30]) != 1440 ||
		binary.BigEndian.Uint16(h[30:32]) != 2560 || binary.BigEndian.Uint32(h[32:36]) != 3 {
		t.Fatal(h)
	}
	a, b := net.Pipe()
	defer b.Close()
	go func() { defer a.Close(); writeMediaError(a, "codec conflicts with H.265") }()
	packet, err := io.ReadAll(b)
	if err != nil || packet[5] != 0 || string(packet[40:]) != "codec conflicts with H.265" ||
		binary.BigEndian.Uint32(packet[32:36]) != uint32(len(packet)-40) {
		t.Fatalf("%v %v", packet, err)
	}
}

func TestUSBPreparationUsesDedicatedCallbackWithoutInputLease(t *testing.T) {
	b := NewBridge()
	calls := 0
	b.prepareUSB = func() error { calls++; return nil }
	w := httptest.NewRecorder()
	b.serveHID(w, httptest.NewRequest("POST", "/api/hid/prepare", nil))
	if w.Code != 204 || calls != 1 || len(b.sessions) != 0 {
		t.Fatalf("prepare: %d %d %d", w.Code, calls, len(b.sessions))
	}
	b.prepareUSB = nil
	w = httptest.NewRecorder()
	b.serveHID(w, httptest.NewRequest("POST", "/api/hid/prepare", nil))
	if w.Code != 503 {
		t.Fatal("missing preparation callback accepted")
	}
}

func TestAbsoluteWheelHeartbeatAndExpiry(t *testing.T) {
	b := NewBridge()
	id := strings.Repeat("a", 32)
	s := &hidSession{id: id, touched: time.Now(), keyboard: make(chan hid.QueuedReport, 2), mouse: make(chan hid.QueuedReport, 2), manual: inputcontrol.NewManualSession(controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff), nil)}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		t.Fatal("input unexpectedly occupied")
	}
	b.sessions[id] = s
	t.Cleanup(b.Stop)
	reports := make(chan []byte, 2)
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		for e := range s.mouse {
			err := e.Execute(func() error { reports <- append([]byte(nil), e.Data...); return nil })
			e.Complete(err == nil)
		}
	}()
	req := httptest.NewRequest("POST", "/api/hid/mouse/absolute", strings.NewReader("{\"buttons\":1,\"x\":12345,\"y\":23456,\"wheel\":-1}"))
	req.Header.Set("X-NanoKVM-Session", id)
	response := httptest.NewRecorder()
	b.serveHID(response, req)
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	report := <-reports
	if len(report) != 7 || report[0] != 1 || report[5] != 255 || binary.LittleEndian.Uint16(report[1:3]) != 12345 ||
		binary.LittleEndian.Uint16(report[3:5]) != 23456 {
		t.Fatal(report)
	}
	s.mu.Lock()
	s.touched = time.Now().Add(-11 * time.Second)
	s.mu.Unlock()
	b.expire()
	if !s.closed.Load() || len(b.sessions) != 0 {
		t.Fatal("expired input was not released")
	}
	// A viewing-only heartbeat must not acquire input or open HID devices.
	req = httptest.NewRequest("POST", "/api/hid/heartbeat", strings.NewReader("{}"))
	req.Header.Set("X-NanoKVM-Session", strings.Repeat("b", 32))
	response = httptest.NewRecorder()
	b.serveHID(response, req)
	if response.Code != 204 || len(b.sessions) != 0 {
		t.Fatal("heartbeat claimed input")
	}
}

func TestCanceledHIDRequestCannotWriteQueuedReportLater(t *testing.T) {
	b := NewBridge()
	id := strings.Repeat("c", 32)
	s := &hidSession{id: id, touched: time.Now(), keyboard: make(chan hid.QueuedReport, 1), mouse: make(chan hid.QueuedReport, 1), manual: inputcontrol.NewManualSession(controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff), nil)}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		t.Fatal("input unexpectedly occupied")
	}
	b.sessions[id] = s
	t.Cleanup(b.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("POST", "/api/hid/mouse/absolute", strings.NewReader(`{"buttons":1,"x":123,"y":456}`)).WithContext(ctx)
	req.Header.Set("X-NanoKVM-Session", id)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); b.serveHID(response, req) }()
	var event hid.QueuedReport
	select {
	case event = <-s.mouse:
	case <-time.After(time.Second):
		t.Fatal("report was not queued")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled request did not return")
	}
	if response.Code != 504 {
		t.Fatal(response.Code, response.Body.String())
	}
	writes := 0
	err := event.Execute(func() error { writes++; return nil })
	event.Complete(err == nil)
	if err != context.Canceled || writes != 0 {
		t.Fatalf("canceled report wrote later: writes=%d err=%v", writes, err)
	}
}

func TestInitialMediaWaitsForEncoderStartupAndBoundsFailure(t *testing.T) {
	calls := 0
	frame, ok := waitInitialMediaFrame(context.Background(), func(context.Context) (stream.VideoFrame, bool) {
		calls++
		if calls < 3 {
			return stream.VideoFrame{Result: -1}, true
		}
		return stream.VideoFrame{Result: 3, Data: []byte{1, 2, 3}}, true
	})
	if !ok || calls != 3 || frame.Result != 3 {
		t.Fatalf("startup rejected transient capture failure: %v %d %+v", ok, calls, frame)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, ok = waitInitialMediaFrame(ctx, func(ctx context.Context) (stream.VideoFrame, bool) { <-ctx.Done(); return stream.VideoFrame{}, false })
	if ok || ctx.Err() != context.DeadlineExceeded {
		t.Fatal("persistent capture failure was not bounded")
	}
}

func TestAndroidPositionsUseRelativeUSBWhenAbsoluteIsDisabled(t *testing.T) {
	b := NewBridge()
	id := strings.Repeat("d", 32)
	s := &hidSession{id: id, touched: time.Now(), keyboard: make(chan hid.QueuedReport, 1), mouse: make(chan hid.QueuedReport, 1), manual: inputcontrol.NewManualSession(controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff), nil), relativeOnly: true, relativePointer: relativePointer{known: true, width: 1920, height: 1080}}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		t.Fatal("input unexpectedly occupied")
	}
	b.sessions[id] = s
	t.Cleanup(b.Stop)
	reports := make(chan []byte, 1)
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		for e := range s.mouse {
			err := e.Execute(func() error { reports <- append([]byte(nil), e.Data...); return nil })
			e.Complete(err == nil)
		}
	}()
	req := httptest.NewRequest("POST", "/api/hid/mouse/absolute", strings.NewReader(`{"buttons":1,"x":100,"y":200,"wheel":-1}`))
	req.Header.Set("X-NanoKVM-Session", id)
	response := httptest.NewRecorder()
	b.serveHID(response, req)
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	report := <-reports
	if len(report) != 5 || report[0] != 1 || report[1] != 5 || report[2] != 6 || report[3] != 255 {
		t.Fatal("Android report did not reach relative USB", report)
	}
}
