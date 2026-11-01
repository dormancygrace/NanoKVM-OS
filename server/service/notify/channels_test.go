package notify

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var sample = Rendered{
	Kind: KindPower, Severity: SeverityWarning, Title: "Host powered off",
	Body: "The host's power LED turned off.", Device: "kvm1",
	Time: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
}

type captured struct {
	method, path, contentType string
	header                    http.Header
	body                      []byte
}

func capture(t *testing.T, status int) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.contentType, got.header = r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type"), r.Header
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"description":"answer"}`))
	}))
	t.Cleanup(server.Close)
	return server, got
}

func channel(kind string, settings map[string]string) Channel {
	ch := Channel{Type: kind, Settings: settings}
	if err := ch.validate(); err != nil {
		panic(err)
	}
	return ch
}

func TestWebhookSignature(t *testing.T) {
	server, got := capture(t, http.StatusNoContent)
	ch := channel("webhook", map[string]string{"url": server.URL + "/hook", "hmacSecret": "k", "authorization": "Bearer t"})
	r := sample
	r.Image = []byte{0xff, 0xd8}
	if err := sendChannel(context.Background(), ch, r); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte("k"))
	mac.Write(got.body)
	if got.header.Get("X-NanoKVM-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("wrong signature")
	}
	var payload webhookPayload
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Event != KindPower || payload.Severity != "warning" || payload.Image != "/9g=" ||
		payload.Time != "2026-10-31T12:00:00Z" || got.header.Get("Authorization") != "Bearer t" {
		t.Fatalf("payload %+v, header %v", payload, got.header)
	}
}

// echo answers with status and the request's address, headers and body, as
// an error page or a proxy can.
func echo(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(status)
		fmt.Fprintf(w, "Upstream error at %s; %s; %v; %s", r.RequestURI, r.URL.Query().Get("sig"), r.Header, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestErrorsHideEchoedSecrets(t *testing.T) {
	refused, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := refused.Addr().String()
	refused.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+closed+r.URL.RequestURI(), http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	server := echo(t, http.StatusInternalServerError)
	forbidden := echo(t, http.StatusUnauthorized)
	for name, tc := range map[string]struct {
		ch        Channel
		permanent bool
	}{
		"telegram token in the path":    {channel("telegram", map[string]string{"token": "1:TEST_CREDENTIAL_SENTINEL", "chatId": "1", "apiUrl": server.URL}), false},
		"slack address":                 {channel("slack", map[string]string{"url": server.URL + "/services/T0/B0/SENTINELSLACKPART?sig=SENTINELQUERY"}), false},
		"gotify token in a header":      {channel("gotify", map[string]string{"server": forbidden.URL, "token": "SENTINEL-gotify"}), true},
		"ntfy topic escaped":            {channel("ntfy", map[string]string{"server": server.URL, "topic": "a b+c/SENTINEL", "token": "tk_SENTINEL"}), false},
		"matrix token in a header":      {channel("matrix", map[string]string{"homeserver": server.URL, "accessToken": "syt_SENTINEL", "roomId": "!r:x"}), false},
		"redirect to a refused address": {channel("telegram", map[string]string{"token": "2:SENTINEL", "chatId": "1", "apiUrl": redirect.URL}), false},
	} {
		err := sendChannel(context.Background(), tc.ch, sample)
		var perm permanentError
		if err == nil || strings.Contains(err.Error(), "SENTINEL") || errors.As(err, &perm) != tc.permanent {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The same through the page's Test button.
	s := newService(t.TempDir() + "/config.json")
	if err := s.Test(context.Background(), channel("telegram", map[string]string{"token": "3:SENTINEL", "chatId": "1", "apiUrl": server.URL}), nil); err == nil || strings.Contains(err.Error(), "SENTINEL") {
		t.Errorf("test: %v", err)
	}
}

func TestHTTPStatus(t *testing.T) {
	for status, isPermanent := range map[int]bool{400: true, 401: true, 404: true, 429: false, 500: false, 503: false} {
		server, _ := capture(t, status)
		err := sendChannel(context.Background(), channel("slack", map[string]string{"url": server.URL}), sample)
		var perm permanentError
		if err == nil || errors.As(err, &perm) != isPermanent || !strings.Contains(err.Error(), "answer") {
			t.Errorf("HTTP %d: %v", status, err)
		}
	}
}

func TestSyslog(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	host, port, _ := net.SplitHostPort(conn.LocalAddr().String())
	if err := sendChannel(context.Background(), channel("syslog", map[string]string{"host": host, "port": port}), sample); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	want := "<28>1 2026-10-31T12:00:00Z kvm1 nanokvm - power - Host powered off: The host's power LED turned off."
	if string(buf[:n]) != want {
		t.Fatalf("got  %q\nwant %q", buf[:n], want)
	}

	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()
	host, port, _ = net.SplitHostPort(listener.Addr().String())
	done := make(chan string, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			done <- ""
			return
		}
		data, _ := io.ReadAll(c)
		done <- string(data)
	}()
	if err := sendChannel(context.Background(), channel("syslog", map[string]string{"host": host, "port": port, "protocol": "tcp"}), sample); err != nil {
		t.Fatal(err)
	}
	if got := <-done; got != fmt.Sprintf("%d %s", len(want), want) {
		t.Fatalf("tcp framing: %q", got)
	}
}

// fakeSMTP is a server without TLS or login: an internal relay.
func fakeSMTP(t *testing.T, reject string) (string, string, *strings.Builder) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var mu sync.Mutex
	transcript := &strings.Builder{}
	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
		say("220 fake")
		data := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			mu.Lock()
			transcript.WriteString(line)
			mu.Unlock()
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case data:
				if cmd == "." {
					data = false
					say("250 queued")
				}
			case strings.HasPrefix(cmd, "EHLO"):
				say("250 fake")
			case reject != "" && strings.HasPrefix(cmd, reject):
				say("550 no such user")
			case strings.HasPrefix(cmd, "DATA"):
				data = true
				say("354 go")
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return host, port, transcript
}

func TestEmailRefusals(t *testing.T) {
	host, port, _ := fakeSMTP(t, "RCPT")
	ch := channel("email", map[string]string{"host": host, "port": port, "security": "none", "from": "k@example.com", "to": "a@example.com"})
	var perm permanentError
	if err := sendChannel(context.Background(), ch, sample); !errors.As(err, &perm) {
		t.Fatalf("a refused recipient: %v", err)
	}

	host, port, _ = fakeSMTP(t, "")
	ch = channel("email", map[string]string{"host": host, "port": port, "from": "k@example.com", "to": "a@example.com"})
	if err := sendChannel(context.Background(), ch, sample); !errors.As(err, &perm) {
		t.Fatalf("STARTTLS asked of a server without it: %v", err)
	}

	host, port, _ = fakeSMTP(t, "")
	ch = channel("email", map[string]string{"host": host, "port": port, "security": "none", "username": "u", "password": "p",
		"from": "k@example.com", "to": "a@example.com"})
	if err := sendChannel(context.Background(), ch, sample); !errors.As(err, &perm) {
		t.Fatalf("a password over a plain connection: %v", err)
	}
}
