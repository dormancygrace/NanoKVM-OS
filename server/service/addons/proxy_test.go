package addons

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// addonServer serves handler on dir/demo.sock, as an add-on would.
func addonServer(t *testing.T, dir string, handler http.HandlerFunc) {
	t.Helper()
	listener, err := net.Listen("unix", dir+"/demo.sock")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
}

func proxyFixture(t *testing.T) (*Proxy, string) {
	t.Helper()
	// Short paths: a unix socket path is limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "addon")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	writeManifest(t, dir, "demo.json", validManifest())
	return &Proxy{Dir: dir, Sockets: dir}, dir
}

func serve(p *Proxy, method, target, body string, header http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range header {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	path := strings.TrimPrefix(target, "/api/addon/demo")
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	p.ServeAddon(w, r, "demo", path, Identity{User: "admin", Role: "admin"})
	return w
}

func TestProxyPassesRequestWithoutCredentials(t *testing.T) {
	p, dir := proxyFixture(t)
	var got *http.Request
	var body string
	addonServer(t, dir, func(w http.ResponseWriter, r *http.Request) {
		got = r
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		http.SetCookie(w, &http.Cookie{Name: "nano-kvm-token", Value: "stolen"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"state":"ok"}}`)
	})
	w := serve(p, http.MethodPost, "/api/addon/demo/settings?x=1", `{"mode":"a"}`, http.Header{
		"Cookie":         {"nano-kvm-token=secret"},
		"Authorization":  {"Bearer secret"},
		"X-Nanokvm-User": {"forged"},
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"ok"`) {
		t.Fatalf("response %d %s", w.Code, w.Body)
	}
	if got.URL.Path != "/settings" || got.URL.RawQuery != "x=1" || got.Method != http.MethodPost || body != `{"mode":"a"}` {
		t.Errorf("add-on got %s %s?%s %q", got.Method, got.URL.Path, got.URL.RawQuery, body)
	}
	if got.Header.Get("Cookie") != "" || got.Header.Get("Authorization") != "" {
		t.Errorf("credentials passed: %v", got.Header)
	}
	if got.Header.Get("X-NanoKVM-User") != "admin" || got.Header.Get("X-NanoKVM-Role") != "admin" {
		t.Errorf("identity %q %q", got.Header.Get("X-NanoKVM-User"), got.Header.Get("X-NanoKVM-Role"))
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Errorf("add-on set a cookie: %s", w.Header().Get("Set-Cookie"))
	}
}

func TestProxyCleansPath(t *testing.T) {
	p, dir := proxyFixture(t)
	var path string
	addonServer(t, dir, func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path })
	w := httptest.NewRecorder()
	p.ServeAddon(w, httptest.NewRequest(http.MethodGet, "/x", nil), "demo", "/a/../../b", Identity{})
	if w.Code != http.StatusOK || path != "/b" {
		t.Errorf("path %q, code %d", path, w.Code)
	}
}

func TestProxyErrors(t *testing.T) {
	p, dir := proxyFixture(t)
	code := func(w *httptest.ResponseRecorder) int {
		var reply struct{ Code int }
		if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
			t.Fatalf("not the server's format: %s", w.Body)
		}
		return reply.Code
	}
	// Not running: no socket yet.
	if w := serve(p, http.MethodGet, "/api/addon/demo/settings", "", nil); w.Code != http.StatusBadGateway || code(w) != -1 {
		t.Errorf("no socket: %d %s", w.Code, w.Body)
	}
	w := httptest.NewRecorder()
	p.ServeAddon(w, httptest.NewRequest(http.MethodGet, "/x", nil), "missing", "/settings", Identity{})
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown add-on: %d", w.Code)
	}
	w = httptest.NewRecorder()
	p.ServeAddon(w, httptest.NewRequest(http.MethodGet, "/x", nil), "../demo", "/settings", Identity{})
	if w.Code != http.StatusNotFound {
		t.Errorf("bad id: %d", w.Code)
	}
	addonServer(t, dir, func(w http.ResponseWriter, r *http.Request) { _, _ = io.ReadAll(r.Body) })
	if w := serve(p, http.MethodPost, "/api/addon/demo/settings", strings.Repeat("x", RequestLimit+1), nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large body: %d", w.Code)
	}
}
