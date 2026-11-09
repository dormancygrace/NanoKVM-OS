package remotemedia

import (
	"bytes"
	"context"

	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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
