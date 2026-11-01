package standby

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func listenerTestCertificate(t *testing.T) (Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("invalid test certificate")
	}
	return Config{Token: strings.Repeat("ab", 32), Certificate: certificate,
		PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})}, roots
}

func TestRuntimeListenerPreservesHTTPSTransportAndOrigin(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "http"
		if secure {
			name = "https"
		}
		t.Run(name, func(t *testing.T) {
			config, roots := listenerTestCertificate(t)
			if !secure {
				config.Certificate = nil
				config.PrivateKey = nil
			}
			tcp, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			listener, err := runtimeListener(tcp, config)
			if err != nil {
				tcp.Close()
				t.Fatal(err)
			}
			state := NewState(config)
			state.Ready()
			restarted := make(chan struct{}, 1)
			var restarts atomic.Int32
			handler := Handler(config, state, func() error {
				restarts.Add(1)
				restarted <- struct{}{}
				return nil
			})
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (r.TLS != nil) != secure {
					t.Errorf("TLS state lost in listener stack: %v", r.TLS)
				}
				handler.ServeHTTP(w, r)
			})}
			stopped := make(chan error, 1)
			go func() { stopped <- server.Serve(listener) }()
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			t.Cleanup(func() {
				transport.CloseIdleConnections()
				server.Close()
				if err := <-stopped; !errors.Is(err, http.ErrServerClosed) {
					t.Error(err)
				}
			})
			origin := name + "://" + tcp.Addr().String()
			request := func(token, suppliedOrigin string) int {
				t.Helper()
				req, err := http.NewRequest("POST", origin+"/standby/start", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Origin", suppliedOrigin)
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				return response.StatusCode
			}
			otherScheme := "https"
			if secure {
				otherScheme = "http"
			}
			for _, bad := range []string{"", otherScheme + "://" + tcp.Addr().String(), name + "://other.invalid"} {
				if code := request(config.Token, bad); code != 403 {
					t.Fatalf("origin %q: %d", bad, code)
				}
			}
			if code := request("wrong", origin); code != 401 {
				t.Fatal(code)
			}
			for i := 0; i < 2; i++ {
				if code := request(config.Token, origin); code != 202 {
					t.Fatalf("same-origin restart: %d", code)
				}
			}
			select {
			case <-restarted:
			case <-time.After(2 * time.Second):
				t.Fatal("restart did not run")
			}
			if restarts.Load() != 1 {
				t.Fatalf("duplicate restart: %d", restarts.Load())
			}
		})
	}
}
