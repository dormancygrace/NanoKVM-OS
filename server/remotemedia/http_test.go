package remotemedia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func TestHTTPExactReadsCacheAndCancellation(t *testing.T) {
	data := httpImage()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("wrong request method or encoding")
		}
		if calls.Load() > 1 && r.Header.Get("If-Match") != `"version-one"` {
			t.Error("missing identity condition")
		}
		rangeResponse(w, r, data)
	}))
	defer server.Close()
	s, err := OpenHTTP(context.Background(), HTTPOptions{URL: server.URL + "/test.iso?secret=hidden"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Name != "test.iso" || s.Size != uint64(len(data)) {
		t.Fatal("wrong metadata")
	}
	read := func(offset uint64, length uint32) {
		t.Helper()
		got, err := s.ReadBlock(offset, length)
		if err != nil || !bytes.Equal(got, data[offset:offset+uint64(length)]) {
			t.Fatalf("read %d/%d: %v", offset, length, err)
		}
	}
	read(0, 512)
	if calls.Load() != 1 {
		t.Fatal("probe was not cached")
	}
	for _, offset := range []uint64{0, 65536, 131072, 196608} {
		read(offset, 65536)
	}
	before := calls.Load()
	read(200000, 4096)
	if calls.Load() != before {
		t.Fatal("cached range fetched again")
	}
	read(uint64(len(data))-2048, 2048)
	read(1048570, 65536)
	read(512, MaxRead)
	read(512, MaxRead)
	if cap(s.buffer) != MaxRead {
		t.Fatal("cache exceeds one MiB")
	}
	for _, tc := range []struct {
		offset uint64
		length uint32
	}{{0, 0}, {0, MaxRead + 1}, {uint64(len(data)), 512}, {^uint64(0) - 1, 512}} {
		if _, err := s.ReadBlock(tc.offset, tc.length); err == nil {
			t.Fatal("invalid read accepted")
		}
	}
	s.Close()
	if _, err := s.ReadBlock(512, 512); !errors.Is(err, context.Canceled) {
		t.Fatal("cached bytes served after close")
	}
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

func TestHTTPImageIdentity(t *testing.T) {
	for _, mode := range []string{"etag-changed", "etag-missing", "size", "modified", "no-validator", "weak-etag"} {
		t.Run(mode, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				second := count.Add(1) > 1
				total := 65536
				if mode == "size" && second {
					total = 131072
				}
				tag := `"v1"`
				if second && mode == "etag-changed" {
					tag = `"v2"`
				}
				if (second && mode == "etag-missing") || mode == "modified" || mode == "no-validator" {
					tag = ""
				}
				if mode == "weak-etag" {
					tag = `W/"v1"`
				}
				if tag != "" {
					w.Header().Set("ETag", tag)
				}
				if mode == "modified" {
					stamp := "Tue, 14 Nov 2023 22:13:20 GMT"
					if second {
						if r.Header.Get("If-Unmodified-Since") != stamp {
							t.Error("missing modification condition")
						}
						stamp = "Wed, 15 Nov 2023 22:13:20 GMT"
					}
					w.Header().Set("Last-Modified", stamp)
				}
				if mode == "weak-etag" && r.Header.Get("If-Match") != "" {
					t.Error("weak tag used for If-Match")
				}
				var first, last int
				_, _ = fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &first, &last)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, total))
				w.WriteHeader(206)
				_, _ = w.Write(make([]byte, last-first+1))
			}))
			defer server.Close()
			s, err := OpenHTTP(context.Background(), HTTPOptions{URL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			_, err = s.ReadBlock(4096, 512)
			wantError := mode != "no-validator" && mode != "weak-etag"
			if (err != nil) != wantError {
				t.Fatalf("read error: %v", err)
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

func TestHTTPStatusErrorsDoNotRetryPermanentFailures(t *testing.T) {
	for code, want := range map[int]string{200: "byte ranges", 401: "denied access", 403: "denied access", 404: "not found", 410: "not found", 412: "image changed", 416: "size or byte range changed", 429: "HTTP 429"} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			data := httpImage()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					rangeResponse(w, r, data)
					return
				}
				w.WriteHeader(code)
			}))
			defer server.Close()
			s, err := OpenHTTP(context.Background(), HTTPOptions{URL: server.URL + "/test.iso?secret=hidden"})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			_, err = s.ReadBlock(4096, 512)
			if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "hidden") {
				t.Fatalf("wrong status error: %v", err)
			}
			if calls.Load() != 2 {
				t.Fatalf("permanent failure retried: %d", calls.Load())
			}
		})
	}
}

func TestHTTPTransientReadRetriesOnceOnFreshConnection(t *testing.T) {
	for _, mode := range []string{"503", "interrupted", "persistent"} {
		t.Run(mode, func(t *testing.T) {
			data := httpImage()
			var calls atomic.Int32
			peers := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				peers <- r.RemoteAddr
				if call == 2 || mode == "persistent" && call > 1 {
					if mode == "interrupted" {
						w.Header().Set("ETag", `"version-one"`)
						w.Header().Set("Content-Range", fmt.Sprintf("bytes 4096-4607/%d", len(data)))
						w.Header().Set("Content-Length", "512")
						w.WriteHeader(206)
						w.(http.Flusher).Flush()
						_, _ = w.Write([]byte("short"))
						w.(http.Flusher).Flush()
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							conn.Close()
						}
						return
					}
					w.WriteHeader(503)
					return
				}
				rangeResponse(w, r, data)
			}))
			defer server.Close()
			s, err := OpenHTTP(context.Background(), HTTPOptions{URL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, err := s.ReadBlock(4096, 512)
			if mode == "persistent" {
				if err == nil {
					t.Fatal("persistent failure accepted")
				}
			} else if err != nil || !bytes.Equal(got, data[4096:4608]) {
				t.Fatalf("retry: %v", err)
			}
			if calls.Load() != 3 {
				t.Fatalf("want two attempts, got %d total requests", calls.Load())
			}
			<-peers
			first, second := <-peers, <-peers
			if first == second {
				t.Fatal("retry reused the old connection")
			}
		})
	}
}

func TestHTTPAttemptsShareOneReadBudget(t *testing.T) {
	data := httpImage()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			rangeResponse(w, r, data)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	s, err := OpenHTTP(context.Background(), HTTPOptions{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.attemptTimeout = 120 * time.Millisecond
	s.readBudget = 180 * time.Millisecond
	start := time.Now()
	if _, err = s.ReadBlock(4096, 512); err == nil {
		t.Fatal("stalled read accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("read exceeded total budget")
	}
	if calls.Load() != 3 {
		t.Fatalf("retry count: %d", calls.Load())
	}
}

func TestHTTPSignedTargetRefreshKeepsImageIdentity(t *testing.T) {
	for _, mode := range []string{"401", "403", "410", "changed-etag", "changed-size", "no-validator"} {
		t.Run(mode, func(t *testing.T) {
			data := httpImage()
			var selectors, targets atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := targets.Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
					t.Error("credentials leaked on refresh")
				}
				if count > 1 && r.URL.Query().Get("token") == "1" {
					code := 401
					if mode == "403" {
						code = 403
					}
					if mode == "410" {
						code = 410
					}
					w.WriteHeader(code)
					return
				}
				tag := `"version-one"`
				body := data
				stamp := time.Unix(1700000000, 0)
				if count > 1 && mode == "changed-etag" {
					tag = `"version-two"`
				}
				if count > 1 && mode == "changed-size" {
					body = append(append([]byte(nil), data...), make([]byte, 2048)...)
				}
				if mode == "no-validator" {
					tag = ""
					stamp = time.Time{}
				}
				if tag != "" {
					w.Header().Set("ETag", tag)
				}
				http.ServeContent(w, r, "test.iso", stamp, bytes.NewReader(body))
			}))
			defer target.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				u, p, ok := r.BasicAuth()
				if !ok || u != "admin" || p != "secret" {
					t.Error("origin credentials missing")
				}
				token := selectors.Add(1)
				http.Redirect(w, r, fmt.Sprintf("%s/test.iso?token=%d", target.URL, token), 302)
			}))
			defer origin.Close()
			s, err := OpenHTTP(context.Background(), HTTPOptions{URL: origin.URL + "/start?private=hidden", Username: "admin", Password: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, err := s.ReadBlock(4096, 512)
			good := mode == "401" || mode == "403" || mode == "410"
			if good && (err != nil || !bytes.Equal(got, data[4096:4608])) {
				t.Fatalf("refresh: %v", err)
			}
			if !good && err == nil {
				t.Fatal("unverified replacement accepted")
			}
			want := int32(2)
			if mode == "no-validator" {
				want = 1
			}
			if selectors.Load() != want {
				t.Fatalf("selector count: %d", selectors.Load())
			}
			if good && s.url.Query().Get("token") != "2" {
				t.Fatal("new target was not retained")
			}
			if err != nil && (strings.Contains(err.Error(), "hidden") || strings.Contains(err.Error(), "secret")) {
				t.Fatal("secret exposed in error")
			}
		})
	}
}
