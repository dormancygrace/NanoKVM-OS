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

	"NanoKVM-IPMI/internal/ipmi"
)

// statusSocket is the add-on's API, named in its manifest
// (/usr/share/nanokvm/addons.d/ipmi.json); the server proxies
// /api/addon/ipmi/... to it for administrators, and reads GET /events for
// its notifications.
const statusSocket = "/run/nanokvm/addons/ipmi.sock"

// status is GET /status of the settings page.
type status struct {
	State         string `json:"state"`
	Port          int    `json:"port"`
	Sessions      int    `json:"sessions"`
	LockedOut     int    `json:"lockedOut"`
	LastCommand   string `json:"lastCommand"`
	LastCommandAt string `json:"lastCommandAt"`
}

func statusOf(stats ipmi.Stats, port int) status {
	s := status{
		State: "listening", Port: port, Sessions: stats.Sessions,
		LockedOut: stats.LockedOut, LastCommand: stats.LastCommand,
	}
	if !stats.LastCommandAt.IsZero() {
		s.LastCommandAt = stats.LastCommandAt.Local().Format("2006-01-02 15:04:05")
	}
	return s
}

// events is GET /events?after=N: the logins and lockouts after sequence
// number N, for the server's security notifications. Epoch changes when the
// daemon restarts and its numbering starts again.
type events struct {
	Epoch  string       `json:"epoch"`
	Latest uint64       `json:"latest"`
	Events []ipmi.Event `json:"events"`
}

func statusHandler(server *ipmi.Server, port int, epoch string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": statusOf(server.Stats(), port)})
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

// serveStatus listens on path, readable by root only: the server, which
// checked the administrator, is the only client.
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
