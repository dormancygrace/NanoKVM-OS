package standby

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStateFailsClosed(t *testing.T) {
	s := NewState(Config{})
	if s.Get().Safe {
		t.Fatal("preparing is not safe")
	}
	for _, magic := range []bool{false, true} {
		if _, e := s.Restart(magic); e == nil {
			t.Fatal("accepted wake during transition")
		}
	}
	s.Fail(errors.New("unmount busy"))
	s.Ready()
	if s.Get().Safe || s.Get().State != "failed" {
		t.Fatal(s.Get())
	}
	if _, e := s.Restart(true); e == nil {
		t.Fatal("magic wake accepted failed transition")
	}
	first, e := s.Restart(false)
	if e != nil || !first {
		t.Fatal("authenticated failure recovery unavailable")
	}
}

// A request that finishes within the wait does not fail the shutdown,
// and none admitted meanwhile can extend it.

func TestMagicPacket(t *testing.T) {
	mac := []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	valid := append(bytes.Repeat([]byte{255}, 6), bytes.Repeat(mac, 16)...)
	if !MagicPacket(valid, mac) || !MagicPacket(append([]byte{1, 2, 3}, valid...), mac) {
		t.Fatal("standard packet rejected")
	}
	for _, b := range [][]byte{valid[:len(valid)-1], bytes.Repeat(mac, 16), nil, []byte("POST /wake")} {
		if MagicPacket(b, mac) {
			t.Fatal("accepted invalid packet")
		}
	}
	for i := range valid {
		b := bytes.Clone(valid)
		b[i] ^= 1
		if MagicPacket(b, mac) {
			t.Fatalf("accepted corrupted byte %d", i)
		}
	}
	if MagicPacket(valid, []byte{2, 1, 2, 3, 4, 5}) || MagicPacket(valid, nil) {
		t.Fatal("wrong NIC")
	}
}
func TestHTTPAuthorizationAndPhases(t *testing.T) {
	c := Config{Token: strings.Repeat("ab", 32)}
	s := NewState(c)
	var calls atomic.Int32
	h := Handler(c, s, func() error { calls.Add(1); return nil })
	run := func(method, path, token, origin string, tlsOn bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://192.0.2.1"+path, nil)
		r.Header.Set("Authorization", token)
		r.Header.Set("Origin", origin)
		if tlsOn {
			r.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	bearer := "Bearer " + c.Token
	for _, token := range []string{"", c.Token, "Bearer wrong"} {
		if w := run("POST", "/standby/start", token, "http://192.0.2.1", false); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	for _, origin := range []string{"", "http://evil.example", "https://192.0.2.1", "http://192.0.2.1/path", "http://admin@192.0.2.1"} {
		if w := run("POST", "/standby/start", bearer, origin, false); w.Code != 403 {
			t.Fatal(origin, w.Code)
		}
	}
	if w := run("POST", "/standby/start", bearer, "http://192.0.2.1", false); w.Code != 409 {
		t.Fatal("transition wake", w.Code)
	}
	if w := run("GET", "/standby/start", bearer, "", false); w.Code != 404 {
		t.Fatal("GET can restart")
	}
	w := run("GET", "/standby/status", bearer, "", false)
	var status Status
	if e := json.Unmarshal(w.Body.Bytes(), &status); e != nil || status.Safe {
		t.Fatal(w.Body.String())
	}
	s.Ready()
	for i := 0; i < 2; i++ {
		if w := run("POST", "/standby/start", bearer, "https://192.0.2.1", true); w.Code != 202 {
			t.Fatal(w.Code)
		}
	}
	time.Sleep(350 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("duplicate reboot", calls.Load())
	}
	if w := run("GET", "/standby", bearer, "", false); strings.Contains(w.Body.String(), c.Token) {
		t.Fatal("key in HTML")
	}
}
