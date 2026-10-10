package mjpeg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"

	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
)

// blockMjpeg makes mjpegAllowed return a value the test changes.
func blockMjpeg(t *testing.T) *bool {
	t.Helper()
	allowed := false
	old := mjpegAllowed
	mjpegAllowed = func() bool { return allowed }
	blockedLogged.Store(false)
	t.Cleanup(func() { mjpegAllowed = old; blockedLogged.Store(false) })
	return &allowed
}

func TestConnectRefusesAndLogsOncePerBlockedPeriod(t *testing.T) {
	allowed := blockMjpeg(t)
	hook := logtest.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	gin.SetMode(gin.TestMode)

	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("GET", "/api/stream/mjpeg", nil)
		Connect(c)

		if recorder.Code != http.StatusConflict {
			t.Fatalf("status %d, want %d", recorder.Code, http.StatusConflict)
		}
		if kind := recorder.Header().Get("Content-Type"); strings.Contains(kind, "multipart") {
			t.Fatalf("refusal is a stream: %q", kind)
		}
		var body struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code == 0 || body.Msg != common.MjpegBlockedMessage {
			t.Fatalf("body %q, %v", recorder.Body.String(), err)
		}
	}
	if entries := hook.AllEntries(); len(entries) != 1 || entries[0].Level != log.InfoLevel {
		t.Fatalf("log entries %v, want one at Info", entries)
	}

	*allowed = true
	if mjpegBlocked() {
		t.Fatal("blocked while allowed")
	}
	*allowed = false
	if !mjpegBlocked() || len(hook.AllEntries()) != 2 {
		t.Fatalf("a new blocked period was not logged: %d entries", len(hook.AllEntries()))
	}
}

func TestRunDoesNotCaptureWhileBlocked(t *testing.T) {
	blockMjpeg(t)
	s := NewStreamer()
	client := newMjpegClient(context.Background())
	s.mutex.Lock()
	s.running = true
	s.clients[client] = struct{}{}
	s.updateClientSnapshotLocked()
	s.mutex.Unlock()

	done := make(chan struct{})
	go func() { s.run(); close(done) }()
	t.Cleanup(func() {
		s.mutex.Lock()
		delete(s.clients, client)
		s.updateClientSnapshotLocked()
		s.mutex.Unlock()
		<-done
	})

	// ReadMjpeg of the test stub fails with -1; only the gate reports -8.
	deadline := time.After(2 * time.Second)
	for {
		for _, status := range stream.LatestCaptureStatuses() {
			if status.Mode == stream.CaptureModeMJPEG && status.Result == common.MjpegBlockedResult {
				if status.Ok || status.Message != common.MjpegBlockedMessage {
					t.Fatalf("status %+v", status)
				}
				return
			}
		}
		select {
		case <-deadline:
			t.Fatal("no blocked capture status published")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
