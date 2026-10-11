package middleware

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"

	"github.com/gorilla/websocket"
)

const SessionRevokedCloseCode = 4401

// session is one authenticated request that is still running, such as a
// websocket.
type session struct {
	username     string
	tokenVersion uint64
	// store is the account store that authenticated the request. The recheck
	// asks the same one rather than reading authn.DefaultStore again.
	store  *authn.Store
	cancel context.CancelFunc
}

// sessionRegistry holds the running sessions, and while there are any, one
// goroutine that rechecks them all every interval against the account store.
// RevokeUserSessions ends a user's sessions at once and covers every change
// made through the server. The recheck is for the rest: an account file
// removed by the reset button or edited from a shell. It costs one stat of
// the file per distinct token while the file is unchanged.
type sessionRegistry struct {
	mutex    sync.Mutex
	nextID   atomic.Uint64
	byUserID map[string]map[uint64]session
	interval time.Duration
	watching bool
}

func newSessionRegistry(interval time.Duration) *sessionRegistry {
	return &sessionRegistry{byUserID: make(map[string]map[uint64]session), interval: interval}
}

var activeSessions = newSessionRegistry(sessionRecheckDelay)

func (r *sessionRegistry) register(entry session) func() {
	id := r.nextID.Add(1)
	r.mutex.Lock()
	if r.byUserID[entry.username] == nil {
		r.byUserID[entry.username] = make(map[uint64]session)
	}
	r.byUserID[entry.username][id] = entry
	if !r.watching && entry.store != nil {
		r.watching = true
		go r.watch()
	}
	r.mutex.Unlock()

	return func() {
		r.mutex.Lock()
		delete(r.byUserID[entry.username], id)
		if len(r.byUserID[entry.username]) == 0 {
			delete(r.byUserID, entry.username)
		}
		r.mutex.Unlock()
	}
}

// watch rechecks the sessions until none are left. register starts it again
// with the next session.
func (r *sessionRegistry) watch() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for range ticker.C {
		r.recheck()

		r.mutex.Lock()
		if len(r.byUserID) == 0 {
			r.watching = false
			r.mutex.Unlock()
			return
		}
		r.mutex.Unlock()
	}
}

// recheck cancels every session whose token the store no longer accepts. The
// store is asked outside the registry's lock, once per distinct token.
func (r *sessionRegistry) recheck() {
	r.mutex.Lock()
	sessions := make([]session, 0, len(r.byUserID))
	for _, byID := range r.byUserID {
		for _, entry := range byID {
			sessions = append(sessions, entry)
		}
	}
	r.mutex.Unlock()

	type tokenKey struct {
		store        *authn.Store
		username     string
		tokenVersion uint64
	}
	valid := make(map[tokenKey]bool)
	for _, entry := range sessions {
		if entry.store == nil {
			continue
		}
		key := tokenKey{entry.store, entry.username, entry.tokenVersion}
		ok, seen := valid[key]
		if !seen {
			_, err := entry.store.ValidateToken(entry.username, entry.tokenVersion)
			ok = err == nil
			valid[key] = ok
		}
		if !ok {
			entry.cancel()
		}
	}
}

func RevokeUserSessions(username string) {
	activeSessions.revoke(username)
}

func (r *sessionRegistry) revoke(username string) {
	r.mutex.Lock()
	sessions := r.byUserID[username]
	delete(r.byUserID, username)
	r.mutex.Unlock()

	for _, entry := range sessions {
		entry.cancel()
	}
}

func WatchWebSocket(ctx context.Context, connection *websocket.Conn) func() {
	stopped := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(SessionRevokedCloseCode, "session expired or revoked"),
				time.Now().Add(2*time.Second),
			)
			_ = connection.Close()
		case <-stopped:
		}
	}()
	return func() { stopOnce.Do(func() { close(stopped) }) }
}

func CheckWebSocketOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || !validOriginURL(parsed) {
		return false
	}
	requestHost, requestScheme := ExternalRequestAddress(request)
	if equalOrigin(parsed, requestHost, requestScheme) {
		return true
	}
	return isAllowedOrigin(parsed)
}

// ExternalRequestAddress returns the host and scheme visible to the client.
// Forwarded headers are only considered when the direct peer is configured as
// a trusted reverse proxy, so clients connected directly cannot spoof them.
func ExternalRequestAddress(request *http.Request) (string, string) {
	conf := config.GetInstance()
	host := request.Host
	scheme := strings.ToLower(conf.Proto)
	if scheme != "https" {
		scheme = "http"
	}
	if request.TLS != nil {
		scheme = "https"
	}
	if !isTrustedProxy(request.RemoteAddr, conf.Security.TrustedProxies) {
		return host, scheme
	}
	if forwarded := validForwardedHost(singleForwardedValue(request.Header.Get("X-Forwarded-Host"))); forwarded != "" {
		host = forwarded
	}
	if forwarded := strings.ToLower(singleForwardedValue(request.Header.Get("X-Forwarded-Proto"))); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return host, scheme
}

func isAllowedOrigin(origin *url.URL) bool {
	for _, allowed := range config.GetInstance().Security.AllowedOrigins {
		parsed, err := url.Parse(strings.TrimSpace(allowed))
		if err == nil && validOriginURL(parsed) && equalOrigin(origin, parsed.Host, parsed.Scheme) {
			return true
		}
	}
	return false
}

func validOriginURL(value *url.URL) bool {
	return (strings.EqualFold(value.Scheme, "http") || strings.EqualFold(value.Scheme, "https")) &&
		value.Host != "" && value.User == nil && (value.Path == "" || value.Path == "/") &&
		value.RawQuery == "" && value.Fragment == ""
}

func validForwardedHost(host string) string {
	if host == "" || strings.ContainsAny(host, "/?#@ \t\r\n") {
		return ""
	}
	return host
}

func singleForwardedValue(value string) string {
	if strings.Contains(value, ",") {
		return ""
	}
	return strings.TrimSpace(value)
}

func isTrustedProxy(remoteAddr string, trustedProxies []string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, trusted := range trustedProxies {
		value := strings.TrimSpace(trusted)
		if value == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Contains(address) {
			return true
		}
		if ip, err := netip.ParseAddr(value); err == nil && ip == address {
			return true
		}
	}
	return false
}

func equalOrigin(origin *url.URL, requestHost, requestScheme string) bool {
	if !strings.EqualFold(normalizeOriginScheme(origin.Scheme), normalizeOriginScheme(requestScheme)) {
		return false
	}
	originHost, originPort := splitHostPort(origin.Host)
	host, port := splitHostPort(requestHost)
	if !strings.EqualFold(originHost, host) {
		return false
	}
	if originPort == "" {
		originPort = defaultPort(origin.Scheme)
	}
	if port == "" {
		port = defaultPort(requestScheme)
	}
	return originPort == port
}

func normalizeOriginScheme(scheme string) string {
	switch strings.ToLower(scheme) {
	case "ws":
		return "http"
	case "wss":
		return "https"
	default:
		return strings.ToLower(scheme)
	}
}

func splitHostPort(value string) (string, string) {
	host, port, err := net.SplitHostPort(value)
	if err == nil {
		return host, port
	}
	return strings.Trim(value, "[]"), ""
}

func defaultPort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "https", "wss":
		return "443"
	default:
		return "80"
	}
}
