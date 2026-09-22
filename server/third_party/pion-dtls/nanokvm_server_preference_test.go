package dtls

import (
	"context"
	"testing"
	"time"

	dtlsnet "github.com/pion/dtls/v3/pkg/net"
	"github.com/pion/transport/v4/dpipe"
)

func TestNanoKVMServerCipherPreferenceAndFallback(t *testing.T) {
	const (
		chacha = TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
		aes    = TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
	)
	tests := []struct {
		name         string
		clientSuites []CipherSuiteID
		want         CipherSuiteID
	}{
		{name: "server prefers ChaCha when client lists AES first", clientSuites: []CipherSuiteID{aes, chacha}, want: chacha},
		{name: "AES-only client falls back", clientSuites: []CipherSuiteID{aes}, want: aes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server, clientErr, serverErr := runNanoKVMHandshake(t, tt.clientSuites, []CipherSuiteID{chacha, aes})
			if clientErr != nil || serverErr != nil {
				t.Fatalf("handshake errors: client=%v server=%v", clientErr, serverErr)
			}
			defer client.Close() //nolint:errcheck
			defer server.Close() //nolint:errcheck
			state, ok := client.ConnectionState()
			if !ok {
				t.Fatal("client connection state unavailable")
			}
			if state.CipherSuiteID != tt.want {
				t.Fatalf("selected cipher = %s, want %s", state.CipherSuiteID, tt.want)
			}
			profile, ok := client.SelectedSRTPProtectionProfile()
			if !ok || profile != SRTP_AES128_CM_HMAC_SHA1_80 {
				t.Fatalf("selected SRTP profile = %v, %v; want AES-CM", profile, ok)
			}
		})
	}
}

func TestNanoKVMServerCipherNoIntersectionFails(t *testing.T) {
	client, server, clientErr, serverErr := runNanoKVMHandshake(t,
		[]CipherSuiteID{TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA},
		[]CipherSuiteID{TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
	)
	if client != nil {
		_ = client.Close()
	}
	if server != nil {
		_ = server.Close()
	}
	if clientErr == nil || serverErr == nil {
		t.Fatalf("no-common-suite handshake unexpectedly succeeded: client=%v server=%v", clientErr, serverErr)
	}
}

func runNanoKVMHandshake(t *testing.T, clientSuites, serverSuites []CipherSuiteID) (*Conn, *Conn, error, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientPipe, serverPipe := dpipe.Pipe()
	type result struct {
		conn *Conn
		err  error
	}
	clientResult := make(chan result, 1)
	go func() {
		conn, err := testClient(ctx, dtlsnet.PacketConnFromConn(clientPipe), clientPipe.RemoteAddr(), &Config{
			CipherSuites:           clientSuites,
			SRTPProtectionProfiles: []SRTPProtectionProfile{SRTP_AES128_CM_HMAC_SHA1_80, SRTP_AEAD_AES_128_GCM},
		}, true)
		clientResult <- result{conn: conn, err: err}
	}()

	server, serverErr := testServer(ctx, dtlsnet.PacketConnFromConn(serverPipe), serverPipe.RemoteAddr(), &Config{
		CipherSuites:           serverSuites,
		SRTPProtectionProfiles: []SRTPProtectionProfile{SRTP_AES128_CM_HMAC_SHA1_80, SRTP_AEAD_AES_128_GCM},
	}, true)
	client := <-clientResult
	return client.conn, server, client.err, serverErr
}
