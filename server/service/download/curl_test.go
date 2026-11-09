package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/pem"
	"fmt"

	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"testing"
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

func TestCurlRejectsUntrustedTLS(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer s.Close()
	f := curlDestination(t)
	if err := downloadCurlTransfer(context.Background(), s.URL, f, nil, nil, nil); err == nil {
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
	if err := downloadCurlTransfer(context.Background(), s.URL, f, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(f.Name())
	if string(body) != "verified AES fallback" {
		t.Fatal("fallback body differs")
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
