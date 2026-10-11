package middleware

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLoopbackHTTPRedirectServesTheChallengeBeforeRedirecting(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	challenge := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "token:"+r.URL.Path)
	})
	go func() {
		_ = ListenAndServeLoopbackHTTPRedirect(addr, "8443", http.NotFoundHandler(), challenge, "/.well-known/acme-challenge/")
	}()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var rsp *http.Response
	for i := 0; i < 50; i++ {
		rsp, err = client.Get("http://" + addr + "/.well-known/acme-challenge/abc")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rsp.Body)
	_ = rsp.Body.Close()
	if rsp.StatusCode != 200 || string(body) != "token:/.well-known/acme-challenge/abc" {
		t.Fatalf("challenge: %d %q", rsp.StatusCode, body)
	}

	rsp, err = client.Get("http://" + addr + "/.well-known/other")
	if err != nil {
		t.Fatal(err)
	}
	_ = rsp.Body.Close()
	if rsp.StatusCode != http.StatusTemporaryRedirect || !strings.HasPrefix(rsp.Header.Get("Location"), "https://127.0.0.1:8443/") {
		t.Fatalf("other path: %d %q", rsp.StatusCode, rsp.Header.Get("Location"))
	}
}
