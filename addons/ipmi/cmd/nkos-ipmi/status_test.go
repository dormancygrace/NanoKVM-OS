package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"NanoKVM-IPMI/internal/ipmi"
)

func TestStatusPage(t *testing.T) {
	handler := statusHandler(&ipmi.Server{Username: "admin", Password: "correct-horse"}, 623)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	var reply struct {
		Code int    `json:"code"`
		Data status `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || reply.Code != 0 {
		t.Fatalf("reply %s: %v", w.Body, err)
	}
	if reply.Data != (status{State: "listening", Port: 623}) {
		t.Fatalf("status %+v", reply.Data)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/status", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST answered %d", w.Code)
	}
}

func TestStatusTime(t *testing.T) {
	at := time.Date(2026, 10, 29, 1, 2, 3, 0, time.Local)
	s := statusOf(ipmi.Stats{LastCommand: "reset", LastCommandAt: at}, 623)
	if s.LastCommand != "reset" || s.LastCommandAt != "2026-10-29 01:02:03" {
		t.Fatalf("%+v", s)
	}
}
