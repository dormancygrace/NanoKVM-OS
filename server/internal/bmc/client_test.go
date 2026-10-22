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

	"github.com/spf13/viper"
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

// These are accepted by the server's actual Viper loader. The daemon must
// use the same port even when the owner formats server.yaml differently.
func TestHTTPPortMatchesServerYAML(t *testing.T) {
	for name, data := range map[string]string{
		"block":   "port:\n  http: 8080\n  https: 443\n",
		"comment": "port: # local listeners\n  http: 8080\n  https: 443\n",
		"flow":    "port: {http: 8080, https: 443}\n",
		"quoted":  "port: {http: '8080', https: 443}\n",
		"alias":   "listeners: &ports {http: 8080, https: 443}\nport: *ports\n",
		"merge":   "defaults: &ports {http: 8080, https: 443}\nport: {<<: *ports}\n",
	} {
		t.Run(name, func(t *testing.T) {
			v := viper.New()
			v.SetConfigType("yaml")
			if err := v.ReadConfig(strings.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			want := v.GetInt("port.http")
			if want != 8080 {
				t.Fatalf("server port = %d", want)
			}
			if got := httpPort([]byte(data)); got != want {
				t.Fatalf("daemon port = %d, server port = %d", got, want)
			}
		})
	}
	for _, data := range []string{"", "port: [", "port: {http: -1}", "port: {http: 65536}", "port: {http: invalid}"} {
		if got := httpPort([]byte(data)); got != 0 {
			t.Fatalf("invalid port %q: %d", data, got)
		}
	}
}
