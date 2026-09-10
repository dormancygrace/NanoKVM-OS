package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func curlDestination(t *testing.T) *os.File {
	t.Helper()
	if _, err := os.Stat(imageCurlPath); err != nil {
		t.Skip("curl is not installed")
	}
	f, err := os.CreateTemp(t.TempDir(), "image-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestCurlTransferIntegrity(t *testing.T) {
	body := bytes.Repeat([]byte("known image bytes\n"), 8192)
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/image", http.StatusFound)
		case "/missing":
			http.Error(w, "not an image", 404)
		case "/short":
			w.Header().Set("Content-Length", "200000")
			w.Write([]byte("truncated"))
		default:
			w.Write(body)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, path string
		expected   []byte
		wantErr    bool
	}{
		{"direct", "/image", nil, false}, {"redirect", "/redirect", sum[:], false},
		{"checksum", "/image", make([]byte, 32), true}, {"missing", "/missing", nil, true}, {"truncated", "/short", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := curlDestination(t)
			err := downloadCurlImage(context.Background(), server.URL+tc.path, f, tc.expected, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v", err)
			}
			if !tc.wantErr {
				got, _ := os.ReadFile(f.Name())
				if !bytes.Equal(got, body) {
					t.Fatal("body differs")
				}
			}
		})
	}
}

func TestCurlRejectsUntrustedTLS(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer s.Close()
	f := curlDestination(t)
	if err := downloadCurlImage(context.Background(), s.URL, f, nil, nil); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
	info, _ := f.Stat()
	if info.Size() != 0 {
		t.Fatal("received body through untrusted TLS")
	}
}

func TestCurlAESOnlyFallbackStillVerifiesTLS(t *testing.T) {
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("verified AES fallback")) }))
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}}
	s.StartTLS()
	defer s.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURL_CA_BUNDLE", ca)
	f := curlDestination(t)
	if err := downloadCurlImage(context.Background(), s.URL, f, nil, nil); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(f.Name())
	if string(body) != "verified AES fallback" {
		t.Fatal("fallback body differs")
	}
}

func TestCurlCancellation(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer s.Close()
	f := curlDestination(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- downloadCurlImage(ctx, s.URL, f, nil, nil) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("curl did not connect")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop curl")
	}
}

func TestCurlResumeValidatesRange(t *testing.T) {
	body := bytes.Repeat([]byte("resumable image\n"), 1024)
	const offset = 3072
	sum := sha256.Sum256(body)
	for _, mode := range []string{"valid", "changed", "wrong-range", "wrong-tag"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "bytes=3072-" || r.Header.Get("If-Range") != `"version1"` {
					t.Error("missing resume constraints")
				}
				w.Header().Set("ETag", `"version1"`)
				if mode == "changed" {
					w.Header().Set("ETag", `"version2"`)
					w.Write(body)
					return
				}
				start := offset
				if mode == "wrong-range" {
					start++
				}
				if mode == "wrong-tag" {
					w.Header().Set("ETag", `"version2"`)
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
				w.Header().Set("Content-Length", fmt.Sprint(len(body)-offset))
				w.WriteHeader(206)
				w.Write(body[offset:])
			}))
			defer s.Close()
			f := curlDestination(t)
			f.Write(body[:offset])
			job := &imageResume{ETag: `"version1"`, Total: int64(len(body))}
			err := downloadCurlTransfer(context.Background(), s.URL, f, sum[:], nil, job)
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(f.Name())
				if !bytes.Equal(got, body) {
					t.Fatal("resumed content differs")
				}
			} else if err == nil {
				t.Fatal("accepted invalid resume response")
			}
			if mode == "changed" {
				info, _ := f.Stat()
				if info.Size() != offset {
					t.Fatal("appended changed full response")
				}
			}
		})
	}
}

func TestResumeMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resume.json")
	job := &imageResume{URL: "https://example.test/a.iso", Path: "/data/.nanokvm-download-123", Filename: "a.iso", ETag: `"abc"`, Total: 100}
	if err := saveImageResume(path, job); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("resume URL metadata must be private")
	}
	if _, err := loadImageResume(path); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"/etc/passwd", "/data/../data/.nanokvm-download-1", "/data/visible.iso"} {
		job.Path = invalid
		saveImageResume(path, job)
		if _, err := loadImageResume(path); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	for _, tag := range []string{`W/"weak"`, "", "\"a\r\nb\""} {
		if strongETag(tag) {
			t.Fatal("invalid ETag accepted")
		}
	}
}

func TestImageFinalHeaders(t *testing.T) {
	f := curlDestination(t)
	io.Copy(f, strings.NewReader("HTTP/1.1 200 Connection established\r\n\r\nHTTP/2 302\r\nContent-Length: 10\r\nETag: \"old\"\r\n\r\nHTTP/2 206\r\nContent-Length: 60\r\nETag: \"new\"\r\nContent-Range: bytes 40-99/100\r\n\r\n"))
	m := readImageHeaders(f.Name())
	if m.Status != 206 || m.Length != 60 || m.ETag != `"new"` || m.Start != 40 || m.Total != 100 {
		t.Fatalf("%+v", m)
	}
}
