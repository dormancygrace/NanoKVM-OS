package network

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
)

func TestEthernetRestartWaitsForFlushedResponse(t *testing.T) {
	dir := t.TempDir()
	originalConfig, originalDisabled, originalVLAN := ethernetConfigFile, ethernetDisabledFile, ethernetVLANFile
	originalRestart := restartEthernet
	ethernetConfigFile = filepath.Join(dir, "eth.nodhcp")
	ethernetDisabledFile = filepath.Join(dir, "eth.disabled")
	ethernetVLANFile = filepath.Join(dir, "eth.vlan")
	t.Cleanup(func() {
		endTrialForTest()
		ethernetConfigFile, ethernetDisabledFile, ethernetVLANFile = originalConfig, originalDisabled, originalVLAN
		restartEthernet = originalRestart
	})

	// A slow SD sync must not race the restart against delivery of the reply.
	enteredSync := filepath.Join(dir, "sync-entered")
	releaseSync := filepath.Join(dir, "sync-release")
	syncProgram := `#!/bin/sh
: > "$NK_TEST_SYNC_ENTERED"
while [ ! -e "$NK_TEST_SYNC_RELEASE" ]; do sleep 0.01; done
`
	if err := os.WriteFile(filepath.Join(dir, "sync"), []byte(syncProgram), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NK_TEST_SYNC_ENTERED", enteredSync)
	t.Setenv("NK_TEST_SYNC_RELEASE", releaseSync)
	t.Cleanup(func() { _ = os.WriteFile(releaseSync, nil, 0o600) })

	flushedResponse := make(chan struct{})
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	restarted := make(chan bool, 1)
	restartEthernet = func() error {
		// The fixed handler cannot start this hook until after Flush. A channel
		// proves that ordering without racing reads of ResponseRecorder.
		select {
		case <-flushedResponse:
			restarted <- true
		default:
			restarted <- false
		}
		return nil
	}
	router := gin.New()
	router.POST("/ethernet", func(c *gin.Context) {
		c.Writer = &ethernetResponseWriter{ResponseWriter: c.Writer, flushed: flushedResponse}
		(&Service{}).SetEthernet(c)
	})
	go func() {
		defer close(done)
		request := httptest.NewRequest("POST", "/ethernet", strings.NewReader(`{"enabled":true,"mode":"dhcp"}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
	}()
	t.Cleanup(func() {
		_ = os.WriteFile(releaseSync, nil, 0o600)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("handler did not stop after releasing sync")
		}
	})
	deadline := time.After(2 * time.Second)
	for {
		if _, err := os.Stat(enteredSync); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("handler did not reach sync")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := os.WriteFile(releaseSync, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return")
	}
	select {
	case flushed := <-restarted:
		if !flushed {
			t.Fatal("Ethernet restart began before the success response was flushed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ethernet restart was not scheduled")
	}
	var response proto.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Code != 0 {
		t.Fatalf("unexpected response: %s (%v)", recorder.Body.String(), err)
	}
}

type ethernetResponseWriter struct {
	gin.ResponseWriter
	flushed chan struct{}
}

func (w *ethernetResponseWriter) Flush() {
	w.ResponseWriter.Flush()
	close(w.flushed)
}
