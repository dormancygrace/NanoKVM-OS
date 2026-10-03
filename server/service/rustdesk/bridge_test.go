package rustdesk

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/service/controlmode"
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
	s := &hidSession{id: id, touched: time.Now(), manual: inputcontrol.NewManualSession(controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff), nil)}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		t.Fatal("input unexpectedly occupied")
	}
	b.sessions[id] = s
	t.Cleanup(b.Stop)
	reports := make(chan []byte, 2)
	s.writeHID = func(_ inputcontrol.ManualReportKind, report []byte) error {
		reports <- append([]byte(nil), report...)
		return nil
	}
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
	released := <-reports
	if len(released) != 7 || released[0] != 0 || released[5] != 0 || binary.LittleEndian.Uint16(released[1:3]) != 12345 {
		t.Fatal("expiry left mouse held or moved pointer", released)
	}
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

func TestCanceledHIDRequestCannotWriteAfterWaitingForInputLock(t *testing.T) {
	b := NewBridge()
	id := strings.Repeat("c", 32)
	s := &hidSession{id: id, touched: time.Now(), manual: inputcontrol.NewManualSession(controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff), nil)}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		t.Fatal("input unexpectedly occupied")
	}
	b.sessions[id] = s
	t.Cleanup(b.Stop)
	writes := 0
	s.writeHID = func(_ inputcontrol.ManualReportKind, _ []byte) error { writes++; return nil }
	locked := make(chan struct{})
	unlock := make(chan struct{})
	blockerDone := make(chan struct{})
	go func() {
		defer close(blockerDone)
		s.manual.Execute(func() error { close(locked); <-unlock; return nil })
	}()
	<-locked
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/api/hid/mouse/absolute", strings.NewReader(`{"buttons":1,"x":123,"y":456}`)).WithContext(ctx)
	req.Header.Set("X-NanoKVM-Session", id)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); b.serveHID(response, req) }()
	cancel()
	close(unlock)
	<-blockerDone
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled request did not return")
	}
	if response.Code != 504 || writes != 0 {
		t.Fatalf("canceled report wrote after lock wait: code=%d writes=%d body=%s", response.Code, writes, response.Body.String())
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
	s := &hidSession{id: id, touched: time.Now(), manual: inputcontrol.NewManualSession(controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff), nil), relativeOnly: true, relativePointer: relativePointer{known: true, width: 1920, height: 1080}}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		t.Fatal("input unexpectedly occupied")
	}
	b.sessions[id] = s
	t.Cleanup(b.Stop)
	reports := make(chan []byte, 2)
	s.writeHID = func(_ inputcontrol.ManualReportKind, report []byte) error {
		reports <- append([]byte(nil), report...)
		return nil
	}
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

func TestFailedUSBWriteReleasesKeyboardAndMouse(t *testing.T) {
	b := NewBridge()
	id := strings.Repeat("e", 32)
	s := &hidSession{id: id, touched: time.Now(), manual: inputcontrol.NewManualSession(controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeOff), nil)}
	if !ws.GetManager().AcquireExternalInput(id, s.close) {
		t.Fatal("input unexpectedly occupied")
	}
	b.sessions[id] = s
	t.Cleanup(b.Stop)
	var reports [][]byte
	s.writeHID = func(_ inputcontrol.ManualReportKind, report []byte) error {
		reports = append(reports, append([]byte(nil), report...))
		if len(reports) == 2 {
			return errors.New("USB unavailable")
		}
		return nil
	}
	send := func(path, body string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("X-NanoKVM-Session", id)
		response := httptest.NewRecorder()
		b.serveHID(response, req)
		return response.Code
	}
	if code := send("/api/hid/keyboard", `{"modifiers":1,"keys":[4]}`); code != 204 {
		t.Fatal(code)
	}
	if code := send("/api/hid/mouse/absolute", `{"buttons":1,"x":123,"y":456}`); code != 503 {
		t.Fatal(code)
	}
	if len(reports) != 4 || len(s.held) != 0 {
		t.Fatalf("failed USB write left held input: reports=%v held=%v", reports, s.held)
	}
	for _, value := range reports[2] {
		if value != 0 {
			t.Fatal("keyboard was not released", reports[2])
		}
	}
	if reports[3][0] != 0 || binary.LittleEndian.Uint16(reports[3][1:3]) != 123 || binary.LittleEndian.Uint16(reports[3][3:5]) != 456 {
		t.Fatal("mouse release changed coordinates", reports[3])
	}
}
