package utils

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPServerTLSProtocols(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		server := NewHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), h2)
		if server.ReadHeaderTimeout != 15*time.Second || server.IdleTimeout != 2*time.Minute || server.WriteTimeout != 0 || server.ReadTimeout != 0 {
			t.Fatal("invalid streaming timeouts")
		}
		ts := httptest.NewUnstartedServer(server.Handler)
		ts.Config = server
		ts.EnableHTTP2 = h2
		ts.StartTLS()
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}}
		response, err := client.Get(ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		expected := 1
		if h2 {
			expected = 2
		}
		if response.ProtoMajor != expected {
			t.Fatalf("protocol %s, h2=%v", response.Proto, h2)
		}
		response.Body.Close()
		client.CloseIdleConnections()
		ts.Close()
	}
}
