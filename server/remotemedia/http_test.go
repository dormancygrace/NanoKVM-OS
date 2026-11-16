package remotemedia

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"

	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The test servers listen on loopback, which image URLs may not reach on the
// device; TestLocalAddressRefused covers that check.
func TestMain(m *testing.M) {
	imageDialer.Control = nil
	os.Exit(m.Run())
}

func TestLocalAddressRefused(t *testing.T) {
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	defer func() { interfaceAddrs = net.InterfaceAddrs }()
	for _, address := range []string{"127.0.0.1:80", "[::1]:443", "0.0.0.0:80", "169.254.1.1:80", "[fe80::1]:80", "224.0.0.1:80", "192.168.1.20:443"} {
		if refuseLocalAddress("tcp", address, nil) == nil {
			t.Errorf("%s allowed", address)
		}
	}
	for _, address := range []string{"192.168.1.10:80", "10.0.0.2:8080", "[2001:db8::1]:443", "93.184.216.34:443"} {
		if err := refuseLocalAddress("tcp", address, nil); err != nil {
			t.Errorf("%s refused: %v", address, err)
		}
	}
	// Wired into the HTTP source: the check runs on every dial, redirects
	// included, and its error reaches the user as is.
	imageDialer.Control = refuseLocalAddress
	defer func() { imageDialer.Control = nil }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rangeResponse(w, r, httpImage()) }))
	defer server.Close()
	if _, err := OpenHTTP(context.Background(), HTTPOptions{URL: server.URL + "/a.iso"}); err == nil || !strings.Contains(err.Error(), "this device") {
		t.Errorf("loopback image opened: %v", err)
	}
}

func httpImage() []byte {
	b := make([]byte, 3*MaxRead+2048)
	for i := range b {
		b[i] = byte((i*37 + i/1009) % 251)
	}
	return b
}

func rangeResponse(w http.ResponseWriter, r *http.Request, data []byte) {
	w.Header().Set("ETag", `"version-one"`)
	http.ServeContent(w, r, "test.iso", time.Unix(1700000000, 0), bytes.NewReader(data))
}

func TestHTTPRejectsMalformedRanges(t *testing.T) {
	for _, name := range []string{"ignored", "wrong-start", "wrong-end", "unknown-size", "huge-size", "unaligned", "short", "extra", "compressed", "wrong-length", "empty", "overflow"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cr, size, code := "bytes 0-2047/65536", 2048, 206
				switch name {
				case "ignored":
					code = 200
				case "wrong-start":
					cr = "bytes 1-2048/65536"
				case "wrong-end":
					cr = "bytes 0-1023/65536"
				case "unknown-size":
					cr = "bytes 0-2047/*"
				case "huge-size":
					cr = "bytes 0-2047/1099511627776"
				case "unaligned":
					cr = "bytes 0-2047/65537"
				case "short":
					size = 2047
				case "extra":
					size = 2049
				case "compressed":
					w.Header().Set("Content-Encoding", "gzip")
				case "wrong-length":
					w.Header().Set("Content-Length", "4096")
				case "empty":
					cr = ""
				case "overflow":
					cr = "bytes 0-2047/18446744073709551616"
				}
				w.Header().Set("Content-Range", cr)
				w.WriteHeader(code)
				w.(http.Flusher).Flush() // exercise bounded chunked bodies too
				_, _ = w.Write(make([]byte, size))
			}))
			defer server.Close()
			if s, err := OpenHTTP(context.Background(), HTTPOptions{URL: server.URL}); err == nil {
				s.Close()
				t.Fatal("bad response accepted")
			}
		})
	}
}

func TestHTTPRedirectCredentialsAndTLS(t *testing.T) {
	data := httpImage()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credentials leaked across origins")
		}
		if r.Header.Get("Referer") != "" {
			t.Error("source URL leaked through Referer")
		}
		rangeResponse(w, r, data)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "" {
			t.Error("signed query leaked through Referer")
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != "admin" || p != "private" {
			t.Error("missing origin credentials")
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/next", 302)
		} else {
			http.Redirect(w, r, target.URL+"/image.iso", 302)
		}
	}))
	defer origin.Close()
	s, err := OpenHTTP(context.Background(), HTTPOptions{URL: origin.URL + "/start?token=private", Username: "admin", Password: "private"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.ReadBlock(4096, 512); err != nil {
		t.Fatal(err)
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rangeResponse(w, r, data) }))
	defer tls.Close()
	if bad, err := OpenHTTP(context.Background(), HTTPOptions{URL: tls.URL + "/?token=secret"}); err == nil {
		bad.Close()
		t.Fatal("untrusted certificate accepted")
	} else if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), tls.URL) {
		t.Fatal("URL exposed in error")
	}
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", 302) }))
	defer loop.Close()
	if bad, err := OpenHTTP(context.Background(), HTTPOptions{URL: loop.URL}); err == nil {
		bad.Close()
		t.Fatal("redirect loop accepted")
	}
}

func TestHTTPRejectsHTTPSDowngrade(t *testing.T) {
	var calls atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		rangeResponse(w, r, httpImage())
	}))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/image.iso", http.StatusFound)
	}))
	defer tls.Close()
	// Trust only this fixture certificate, so the failure exercises the redirect
	// policy rather than failing the initial TLS handshake.
	original := http.DefaultTransport
	trusted := original.(*http.Transport).Clone()
	trusted.TLSClientConfig = tls.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = trusted
	defer func() { http.DefaultTransport = original; trusted.CloseIdleConnections() }()
	if source, err := OpenHTTP(context.Background(), HTTPOptions{URL: tls.URL}); err == nil {
		source.Close()
		t.Fatal("HTTPS downgrade accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("insecure redirect target received a request")
	}
}

// A TLS 1.2 server with AES alone must still mount, through the default suites.
func TestHTTPStreamAndCipherFallback(t *testing.T) {
	data := httpImage()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rangeResponse(w, r, data) }))
	server.TLS = &tls.Config{MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256}}
	server.StartTLS()
	defer server.Close()
	original := http.DefaultTransport
	trusted := original.(*http.Transport).Clone()
	trusted.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = trusted
	s, err := OpenHTTP(context.Background(), HTTPOptions{URL: server.URL})
	http.DefaultTransport = original
	if err != nil || s.chacha {
		t.Fatal("AES-only server did not get the default suites", err)
	}
	defer s.Close()
	readStream(t, s.ReadBlock, data, 0, len(data))
}

func TestHTTPOptionsAndRangeParsing(t *testing.T) {
	for _, raw := range []string{"file:///tmp/test.iso", "ftp://host/test.iso", "http://u:p@host/test.iso", "https://host/a#fragment", "http://host:0/a", "http://host:65536/a", "http:///a", "https://host:wrong/a"} {
		if ValidateHTTPOptions(HTTPOptions{URL: raw}) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, value := range []string{"bytes 0-0/0", "bytes 2-1/10", "bytes 0-10/10", "bytes 0-1/3 junk", "bytes -1-2/3"} {
		if _, _, _, ok := parseContentRange(value); ok {
			t.Errorf("accepted %s", value)
		}
	}
	if ValidateHTTPOptions(HTTPOptions{URL: "http://192.168.1.2/image.iso"}) != nil {
		t.Fatal("LAN sources must remain usable")
	}
}
