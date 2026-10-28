package addons

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	pathpkg "path"
	"strings"
	"time"
)

// RequestLimit bounds a request body passed to an add-on.
const RequestLimit = 1 << 20

// proxyTimeout bounds an add-on's answer: these are settings pages, not
// streams or long jobs.
const proxyTimeout = 30 * time.Second

// Identity is the signed-in user a request comes from. The add-on gets it
// as X-NanoKVM-User and X-NanoKVM-Role; the server checked the login.
type Identity struct {
	User string
	Role string
}

// Proxy passes an administrator's request for /api/addon/<id>/<path> to the
// add-on's socket as /<path>. Only the manifest chooses the socket. The
// browser's credentials stay with the server: cookies and the Authorization
// header are not passed on, and the add-on cannot set cookies on the web UI.
type Proxy struct {
	// Dir is the manifest directory (ManifestDir; tests use another).
	Dir string
	// Sockets, when set, replaces SocketDir (tests).
	Sockets string
}

// ServeAddon answers one request; path is the part after /api/addon/<id>.
func (p *Proxy) ServeAddon(w http.ResponseWriter, r *http.Request, id, path string, who Identity) {
	m, ok := Find(p.Dir, id)
	if !ok || m.Backend == nil {
		writeError(w, http.StatusNotFound, "no such add-on")
		return
	}
	socket := m.SocketPath()
	if p.Sockets != "" {
		socket = p.Sockets + "/" + m.Backend.Socket
	}
	path = pathpkg.Clean("/" + strings.TrimPrefix(path, "/"))
	if r.ContentLength > RequestLimit {
		writeError(w, http.StatusRequestEntityTooLarge, "request too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, RequestLimit)
	ctx, cancel := context.WithTimeout(r.Context(), proxyTimeout)
	defer cancel()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme = "http"
			out.URL.Host = "addon"
			out.Host = "addon"
			out.URL.Path = path
			out.URL.RawPath = ""
			out.URL.RawQuery = r.URL.RawQuery
			for _, header := range []string{"Cookie", "Authorization", "X-NanoKVM-User", "X-NanoKVM-Role"} {
				out.Header.Del(header)
			}
			out.Header.Set("X-NanoKVM-User", who.User)
			out.Header.Set("X-NanoKVM-Role", who.Role)
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
			ResponseHeaderTimeout: proxyTimeout,
			DisableKeepAlives:     true,
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			resp.Header.Set("Cache-Control", "no-store")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "request too large")
				return
			}
			writeError(w, http.StatusBadGateway, "the add-on is not running")
		},
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

// writeError answers in the server's response format.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": -1, "msg": msg})
}
