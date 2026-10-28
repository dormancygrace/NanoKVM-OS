package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"NanoKVM-IPMI/internal/ipmi"
)

// statusSocket is the add-on's API, named in its manifest
// (/usr/share/nanokvm/addons.d/ipmi.json); the server proxies
// /api/addon/ipmi/... to it for administrators.
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

func statusHandler(server *ipmi.Server, port int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": statusOf(server.Stats(), port)})
	})
	return mux
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
		Handler:           statusHandler(server, port),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	go func() { _ = httpServer.Serve(listener) }()
	return nil
}
