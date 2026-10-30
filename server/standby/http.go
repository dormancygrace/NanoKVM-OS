package standby

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed page.html
var Page []byte

func Handler(c Config, state *State, restart func() error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if r.Method == "GET" && r.URL.Path == "/standby.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Write(pageJS)
			return
		}
		if r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/standby") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(Page)
			return
		}
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || len(c.Token) != 64 || subtle.ConstantTimeCompare([]byte(auth), []byte(c.Token)) != 1 {
			http.Error(w, "Use the shutdown browser's restart key.", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/standby/status":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(state.Get())
		case r.Method == "POST" && r.URL.Path == "/standby/start":
			// No cookies, URL tokens, forwarded-host trust or cross-origin writes.
			origin, err := url.Parse(r.Header.Get("Origin"))
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if err != nil || origin.Scheme != scheme || origin.Host != r.Host ||
				origin.User != nil || origin.Path != "" || origin.RawQuery != "" ||
				origin.Fragment != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "same-origin request required", http.StatusForbidden)
				return
			}
			first, err := state.Restart(false)
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			if first {
				go func() {
					// Let the reply leave the socket before restarting.
					time.Sleep(250 * time.Millisecond)
					if err := restart(); err != nil {
						state.RestartFailed(err)
					}
				}()
			}
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

//go:embed page.js
var pageJS []byte
