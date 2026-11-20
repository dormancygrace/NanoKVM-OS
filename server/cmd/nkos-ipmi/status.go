package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"NanoKVM-Server/internal/ipmi"
)

// events is GET /events?after=N: the logins and lockouts after sequence
// number N, for the server's security notifications. Epoch changes when the
// daemon restarts and its numbering starts again.
type events struct {
	Epoch  string       `json:"epoch"`
	Latest uint64       `json:"latest"`
	Events []ipmi.Event `json:"events"`
}

// statusHandler serves the server's two questions: GET /status for the
// BMC settings page and GET /events for the security notifications.
func statusHandler(server *ipmi.Server, port int, epoch string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0,
			"data": ipmi.Status{Port: port, Stats: server.Stats()}})
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		list, latest := server.Events(after)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0,
			"data": events{Epoch: epoch, Latest: latest, Events: list}})
	})
	return mux
}

// newEpoch names this run of the daemon.
func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// serveStatus listens on path, readable by root only: the server is the only
// client.
func serveStatus(path string, server *ipmi.Server, port int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// A socket left by a daemon that did not stop cleanly.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		return err
	}
	httpServer := &http.Server{
		Handler:           statusHandler(server, port, newEpoch()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	go func() { _ = httpServer.Serve(listener) }()
	return nil
}
