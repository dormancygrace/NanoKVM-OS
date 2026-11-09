package mcpservice

import (
	"context"

	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/inputcontrol"
)

type fakeSnapshotter struct {
	snapshot Snapshot
	err      error
}

func (s fakeSnapshotter) Capture(context.Context, SnapshotRequest) (Snapshot, error) {
	return s.snapshot, s.err
}

type fakeHostStatus struct{}

func (fakeHostStatus) HostStatus(context.Context) HostState {
	return HostState{Power: HostPower{PowerButton: true, ResetButton: true, Known: true, On: true},
		Video: HostVideo{Capture: true, Signal: true}, Keyboard: HostKeyboard{Known: true, NumLock: true}}
}

type fakeTextReader struct{ off bool }

func (r fakeTextReader) Available() bool { return !r.off }

func (fakeTextReader) Languages() []string { return []string{"latin", "chinese", "cyrillic"} }

func (fakeTextReader) ReadText(context.Context, string, *ScreenBox) (ScreenText, error) {
	return ScreenText{Language: "cyrillic", Width: 1920, Height: 1080, Text: "Привет, мир",
		Pieces: []ScreenTextPiece{{Text: "Привет, мир", Score: 0.9, Box: ScreenBox{X: 0.4, Y: 0.5, W: 0.1, H: 0.025}}}}, nil
}

// Stateless transport has no session to delete.

func TestMCPHandlerRejectsCrossOriginBrowserRequests(t *testing.T) {
	control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP)
	handler := http.NewCrossOriginProtection().Handler(newMCPHandler(control, &inputcontrol.Coordinator{}, newRemoteWithHID(&recordingHID{}), nil, nil, nil, HostControl{}))

	request := func(origin string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://nanokvm.local/api/mcp", strings.NewReader(`{}`))
		req.Header.Set("Origin", origin)
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	if got := request("https://evil.example").Code; got != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", got)
	}
	if got := request("http://nanokvm.local").Code; got == http.StatusForbidden {
		t.Fatalf("same-origin request was rejected with status %d", got)
	}
}

func TestMCPHandlerRejectsOversizedRequestBody(t *testing.T) {
	control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP)
	handler := newMCPHandler(control, &inputcontrol.Coordinator{}, newRemoteWithHID(&recordingHID{}), nil, nil, nil, HostControl{})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/mcp",
		strings.NewReader(strings.Repeat(" ", maxRequestBodyBytes+1)),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("unexpected oversized response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
