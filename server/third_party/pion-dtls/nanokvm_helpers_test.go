// SPDX-FileCopyrightText: The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

// Test helpers copied from the upstream conn_test.go, which NanoKVM does not keep.

package dtls

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/pion/dtls/v3/pkg/crypto/selfsign"
	dtlsnet "github.com/pion/dtls/v3/pkg/net"
)

func pipeConn(ca, cb net.Conn) (*Conn, *Conn, error) {
	type result struct {
		c   *Conn
		err error
	}

	resultCh := make(chan result, 1) // Buffered to prevent goroutine leak
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Setup client
	go func() {
		client, err := testClient(ctx, dtlsnet.PacketConnFromConn(ca), ca.RemoteAddr(), &Config{
			SRTPProtectionProfiles: []SRTPProtectionProfile{SRTP_AES128_CM_HMAC_SHA1_80},
		}, true)
		resultCh <- result{client, err}
	}()

	// Setup server
	server, err := testServer(ctx, dtlsnet.PacketConnFromConn(cb), cb.RemoteAddr(), &Config{
		SRTPProtectionProfiles: []SRTPProtectionProfile{SRTP_AES128_CM_HMAC_SHA1_80},
	}, true)
	if err != nil {
		// Read from resultCh to prevent goroutine leak
		if res := <-resultCh; res.c != nil {
			_ = res.c.Close()
		}

		return nil, nil, err
	}

	// Receive client
	res := <-resultCh
	if res.err != nil {
		_ = server.Close()

		return nil, nil, res.err
	}

	return res.c, server, nil
}

func testClient(
	ctx context.Context,
	pktConn net.PacketConn,
	rAddr net.Addr,
	cfg *Config,
	generateCertificate bool,
) (*Conn, error) {
	if generateCertificate {
		clientCert, err := selfsign.GenerateSelfSigned()
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{clientCert}
	}
	cfg.InsecureSkipVerify = true
	conn, err := Client(pktConn, rAddr, cfg)
	if err != nil {
		return nil, err
	}

	return conn, conn.HandshakeContext(ctx)
}

func testServer(
	ctx context.Context,
	c net.PacketConn,
	rAddr net.Addr,
	cfg *Config,
	generateCertificate bool,
) (*Conn, error) {
	if generateCertificate {
		serverCert, err := selfsign.GenerateSelfSigned()
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{serverCert}
	}
	conn, err := Server(c, rAddr, cfg)
	if err != nil {
		return nil, err
	}

	return conn, conn.HandshakeContext(ctx)
}
