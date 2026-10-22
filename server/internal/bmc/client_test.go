package bmc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientAgainstHTTP(t *testing.T) {
	var pressed ATXPress
	var holding atomic.Bool // the server holds the button for the press
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && holding.Load() {
			time.Sleep(300 * time.Millisecond)
		}
		if r.URL.Path != ATXPath || r.Header.Get(internalTokenHeader) != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(ATXState{PowerButton: true, PowerLED: true, PowerOn: true})
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&pressed)
			if pressed.Button == "reset" {
				w.WriteHeader(http.StatusNotImplemented)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Client{Addr: strings.TrimPrefix(server.URL, "http://"), TokenFile: token, Timeout: 5 * time.Second}
	state, err := c.State(context.Background())
	if err != nil || !state.PowerOn || !state.PowerButton {
		t.Fatalf("state %+v %v", state, err)
	}
	if err := c.Press(context.Background(), "power", 800); err != nil || pressed != (ATXPress{Button: "power", DurationMs: 800}) {
		t.Fatalf("press %+v %v", pressed, err)
	}
	if err := c.Press(context.Background(), "reset", 800); !errors.Is(err, ErrNoReset) {
		t.Fatalf("reset without a line: %v", err)
	}
	// The server answers a press when the button is released: a hold longer
	// than the client's own timeout must still get its answer.
	holding.Store(true)
	c.Timeout = 100 * time.Millisecond
	if err := c.Press(context.Background(), "power", 300); err != nil {
		t.Fatalf("press as long as the hold: %v", err)
	}
	c.TokenFile = filepath.Join(t.TempDir(), "absent")
	if _, err := c.State(context.Background()); err == nil {
		t.Fatal("no token, no error")
	}
}
