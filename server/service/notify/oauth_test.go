package notify

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTLSSMTP takes mail over implicit TLS and logs in with XOAUTH2 only
// when the bearer token is "good"; any other gets the error challenge, then
// 535 after the client's empty answer.
func fakeTLSSMTP(t *testing.T) (string, string, func() string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var mu sync.Mutex
	var transcript strings.Builder
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
				say("220 fake")
				data, challenged := false, false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					mu.Lock()
					transcript.WriteString(line)
					mu.Unlock()
					cmd := strings.TrimSpace(line)
					switch {
					case data:
						if cmd == "." {
							data = false
							say("250 queued")
						}
					case challenged:
						challenged = false
						say("535 5.7.8 token refused")
					case strings.HasPrefix(cmd, "EHLO"):
						say("250-fake")
						say("250 AUTH XOAUTH2")
					case strings.HasPrefix(cmd, "AUTH XOAUTH2 "):
						raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(cmd, "AUTH XOAUTH2 "))
						if strings.Contains(string(raw), "auth=Bearer good\x01") {
							say("235 accepted")
						} else {
							challenged = true
							say("334 eyJzdGF0dXMiOiI0MDEifQ==")
						}
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
			}(c)
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return host, port, func() string { mu.Lock(); defer mu.Unlock(); return transcript.String() }
}

// fakeTokens answers refresh requests with answer, counting them.
func fakeTokens(t *testing.T, status int, answer string) *int {
	t.Helper()
	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("client_secret") != "S" {
			t.Errorf("refresh request %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	old := googleTokenURL
	googleTokenURL = server.URL
	t.Cleanup(func() { googleTokenURL = old })
	return &count
}

func gmailService(t *testing.T, host, port string) *Service {
	t.Helper()
	s := newService(filepath.Join(t.TempDir(), "config.json"))
	s.sleep = func(context.Context, time.Duration) bool { return true }
	s.config.Channels = []Channel{{ID: "c1", Type: "email", Enabled: true, Settings: map[string]string{
		"provider": "gmail", "host": host, "port": port, "security": "tls", "skipVerify": "true",
		"auth": "oauth2", "oauthClientId": "id", "oauthClientSecret": "S", "oauthToken": "R1",
		"oauthAccount": "me@gmail.com", "from": "me@gmail.com", "to": "a@example.com",
	}}}
	return s
}

func TestXOAUTH2RenewsAndKeepsTheRotatedToken(t *testing.T) {
	host, port, transcript := fakeTLSSMTP(t)
	count := fakeTokens(t, 200, `{"access_token":"good","expires_in":3600,"refresh_token":"R2"}`)
	s := gmailService(t, host, port)
	for range 2 {
		if err := sendChannel(context.Background(), s.config.Channels[0], sample); err != nil {
			t.Fatal(err)
		}
	}
	login := base64.StdEncoding.EncodeToString([]byte("user=me@gmail.com\x01auth=Bearer good\x01\x01"))
	if !strings.Contains(transcript(), "AUTH XOAUTH2 "+login) {
		t.Fatalf("no XOAUTH2 login in\n%s", transcript())
	}
	if *count != 1 {
		t.Fatalf("%d token requests for two messages", *count)
	}
	stored, _ := Load(s.path)
	if len(stored.Channels) != 1 || stored.Channels[0].Settings["oauthToken"] != "R2" {
		t.Fatalf("the renewed refresh token was not kept: %+v", stored.Channels)
	}
}

func TestRevokedSignInShowsOnTheChannel(t *testing.T) {
	host, port, _ := fakeTLSSMTP(t)
	fakeTokens(t, 400, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	s := gmailService(t, host, port)
	s.config.Channels[0].Settings["oauthToken"] = "R-revoked"
	err := sendChannel(context.Background(), s.config.Channels[0], sample)
	var perm permanentError
	if !errors.As(err, &perm) {
		t.Fatalf("a revoked sign-in is retried: %v", err)
	}
	s.active, s.inFlight["c1"] = 1, 1
	s.deliver(s.config.Channels[0], sample)
	got := s.Status().Channels[0].AuthError
	if !strings.Contains(got, "sign in again") || strings.Contains(got, "R-revoked") {
		t.Fatalf("channel status %q", got)
	}
}

func TestEmailAuthSettings(t *testing.T) {
	// A channel stored before sign-ins keeps its password login.
	old := Channel{Type: "email", Settings: map[string]string{"host": "smtp.example.com", "username": "u", "password": "p",
		"from": "k@example.com", "to": "a@example.com"}}
	if err := old.validate(); err != nil || old.Settings["password"] != "p" || old.Settings["auth"] != "password" || old.Settings["provider"] != "custom" {
		t.Fatalf("old channel: %v %v", err, old.Settings)
	}
	// Switching to a password drops the sign-in.
	ch := Channel{Type: "email", Settings: map[string]string{"provider": "gmail", "host": "smtp.gmail.com", "auth": "password",
		"password": "p", "oauthToken": "R1", "oauthClientId": "id", "from": "k@example.com", "to": "a@example.com"}}
	if err := ch.validate(); err != nil || ch.Settings["oauthToken"] != "" || ch.Settings["oauthClientId"] != "" {
		t.Fatalf("password channel: %v %v", err, ch.Settings)
	}
	rejected := map[string]map[string]string{
		"no client":       {"provider": "gmail", "auth": "oauth2", "oauthClientSecret": "S"},
		"no secret":       {"provider": "gmail", "auth": "oauth2", "oauthClientId": "id"},
		"no OAuth there":  {"provider": "yahoo", "auth": "oauth2", "oauthClientId": "id"},
		"tenant in a URL": {"provider": "microsoft365", "auth": "oauth2", "oauthClientId": "id", "oauthTenant": "x/../y"},
	}
	for name, settings := range rejected {
		settings["host"], settings["from"], settings["to"] = "h", "k@example.com", "a@example.com"
		ch := Channel{Type: "email", Settings: settings}
		if err := ch.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ms := Channel{Type: "email", Settings: map[string]string{"provider": "outlook", "host": "h", "auth": "oauth2",
		"oauthClientId": "id", "oauthClientSecret": "dropped", "from": "k@example.com", "to": "a@example.com"}}
	if err := ms.validate(); err != nil || ms.Settings["oauthTenant"] != "consumers" || ms.Settings["oauthClientSecret"] != "" {
		t.Fatalf("outlook: %v %v", err, ms.Settings)
	}
}

// A Google revoke ends the account's grant to the whole project: signing in
// again, an abandoned sign-in and signing out while another channel keeps a
// Google sign-in (even through another client) revoke nothing; signing out of
// the last one does.
func TestGoogleGrantRevokedOnlyWhenUnused(t *testing.T) {
	var revoked []string
	old := revokeLater
	revokeLater = func(_ oauthClient, token string) { revoked = append(revoked, token) }
	t.Cleanup(func() { revokeLater = old })
	s := gmailService(t, "h", "465")
	// The page's channels, each with its oauthToken field as given: "" keeps
	// the stored sign-in, "-" signs out.
	page := func(tokens ...string) Settings {
		settings := Settings{Language: "en"}
		for i, token := range tokens {
			ch := s.config.Channels[i].view().Channel
			var clear []string
			if token == "-" {
				token, clear = "", []string{"oauthToken"}
			}
			ch.Settings["oauthToken"] = token
			settings.Channels = append(settings.Channels, ChannelUpdate{Channel: ch, Clear: clear})
		}
		return settings
	}
	oauth.mu.Lock()
	oauth.pending["pending:again"] = &pendingSignIn{token: "R2", expires: time.Now().Add(time.Hour)}
	oauth.pending["pending:abandoned"] = &pendingSignIn{token: "R3", expires: time.Now().Add(-time.Second)}
	oauth.mu.Unlock()

	if err := s.Configure(page("pending:again")); err != nil {
		t.Fatal(err)
	}
	if got := s.config.Channels[0].Settings["oauthToken"]; got != "R2" || len(revoked) != 0 {
		t.Fatalf("sign in again: stored %q, revoked %v", got, revoked)
	}
	// A second channel signed in through another client, maybe of the same
	// project.
	other := s.config.Channels[0]
	other.ID, other.Settings = "c2", maps.Clone(other.Settings)
	other.Settings["oauthClientId"], other.Settings["oauthToken"] = "id2", "R9"
	s.config.Channels = append(s.config.Channels, other)
	if err := s.Configure(page("-", "")); err != nil {
		t.Fatal(err)
	}
	if got := s.config.Channels[1].Settings["oauthToken"]; got != "R9" || len(revoked) != 0 {
		t.Fatalf("sign out beside another Google sign-in: kept %q, revoked %v", got, revoked)
	}
	if err := s.Configure(page("-", "-")); err != nil {
		t.Fatal(err)
	}
	if len(revoked) != 1 || revoked[0] != "R9" {
		t.Fatalf("sign out of the last one: revoked %v", revoked)
	}
}
