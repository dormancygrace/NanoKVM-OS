// Copyright 2026 The NanoKVM OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// tls-records qualifies small TLS records over an isolated test connection.
// The test server reports its own user/system CPU, excluding the handshake.
// The host client verifies every payload and records ping RTT separately.
// The application server and its TLS configuration are never changed.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"slices"
	"time"
)

const marker = 0xc9061305

var sizes = []int{1, 5, 16, 64, 128, 512, 1400, 16384}
var focused = flag.Bool("focused", false, "longer follow-up for 5, 128, 1400 and 16384-byte records")

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func pattern(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte((i*37 + (i >> 7) + n*11) ^ 0xa5)
	}
	return p
}
func write(c net.Conn, p []byte) {
	n, e := c.Write(p)
	must(e)
	if n != len(p) {
		panic("short write")
	}
}
func read(c net.Conn, p []byte) { _, e := io.ReadFull(c, p); must(e) }
func suiteOK(s tls.ConnectionState) bool {
	return (s.Version == tls.VersionTLS13 && s.CipherSuite == tls.TLS_CHACHA20_POLY1305_SHA256) ||
		(s.Version == tls.VersionTLS12 && s.CipherSuite == tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256)
}
func serve(addr string) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(e)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(9061305), Subject: pkix.Name{CommonName: "nanokvm-poly-test"},
		DNSNames: []string{"nanokvm-poly-test"}, NotBefore: time.Now().Add(-24 * time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	must(e)
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS13, DynamicRecordSizingDisabled: true,
		CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256},
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	ln, e := net.Listen("tcp4", addr)
	must(e)
	defer ln.Close()
	must(json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": true, "address": ln.Addr().String(), "pid": os.Getpid(), "certificate_der": base64.StdEncoding.EncodeToString(der)}))
	for connection := 0; connection < 2; connection++ {
		must(ln.(*net.TCPListener).SetDeadline(time.Now().Add(2 * time.Minute)))
		raw, e := ln.Accept()
		must(e)
		must(raw.(*net.TCPConn).SetNoDelay(true))
		c := tls.Server(raw, cfg)
		must(c.SetDeadline(time.Now().Add(2 * time.Minute)))
		must(c.Handshake())
		if !suiteOK(c.ConnectionState()) {
			panic("unexpected cipher suite")
		}
		var h [12]byte
		for {
			must(c.SetDeadline(time.Now().Add(2 * time.Minute)))
			read(c, h[:])
			n, count, mode := int(binary.BigEndian.Uint32(h[:4])), int(binary.BigEndian.Uint32(h[4:8])), int(binary.BigEndian.Uint32(h[8:]))
			if n == 0 {
				break
			}
			if n > 16384 || count < 1 || count > 16384 || mode > 2 {
				panic("invalid test command")
			}
			data := pattern(n)
			buf := make([]byte, n)
			write(c, []byte{0x90})
			u0, s0 := cpuTime()
			w0 := time.Now()
			for i := 0; i < count; i++ {
				if mode != 1 {
					read(c, buf)
					if !bytes.Equal(buf, data) {
						panic("upload mismatch")
					}
				}
				if mode != 0 {
					write(c, data)
				}
			}
			u1, s1 := cpuTime()
			wall := time.Since(w0)
			var stats [64]byte
			values := []uint64{marker, uint64(n), uint64(count), uint64(mode), u1 - u0, s1 - s0, uint64(wall.Nanoseconds()), 1}
			for i, x := range values {
				binary.BigEndian.PutUint64(stats[i*8:], x)
			}
			write(c, stats[:])
		}
		must(c.Close())
	}
	fmt.Println("TLS_SERVER_COMPLETE")
}
func client(addr, certPath string) {
	der, e := os.ReadFile(certPath)
	must(e)
	cert, e := x509.ParseCertificate(der)
	must(e)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		raw, e := net.DialTimeout("tcp4", addr, 10*time.Second)
		must(e)
		must(raw.(*net.TCPConn).SetNoDelay(true))
		c := tls.Client(raw, &tls.Config{MinVersion: version, MaxVersion: version, ServerName: "nanokvm-poly-test", RootCAs: roots,
			DynamicRecordSizingDisabled: true, CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256}})
		must(c.SetDeadline(time.Now().Add(2 * time.Minute)))
		must(c.Handshake())
		if !suiteOK(c.ConnectionState()) {
			panic("unexpected cipher suite")
		}
		for _, n := range sizes {
			for mode := 0; mode < 3; mode++ {
				count := 1024
				if n >= 1400 {
					count = 256
				}
				if *focused {
					count = 4096
					if n >= 1400 {
						count = 2048
					}
					if n >= 16384 {
						count = 512
					}
				}
				data, buf := pattern(n), make([]byte, n)
				var h [12]byte
				binary.BigEndian.PutUint32(h[:4], uint32(n))
				binary.BigEndian.PutUint32(h[4:8], uint32(count))
				binary.BigEndian.PutUint32(h[8:], uint32(mode))
				must(c.SetDeadline(time.Now().Add(2 * time.Minute)))
				write(c, h[:])
				var ready [1]byte
				read(c, ready[:])
				if ready[0] != 0x90 {
					panic("ready marker")
				}
				rtt := make([]int64, 0, count)
				start := time.Now()
				for i := 0; i < count; i++ {
					before := time.Now()
					if mode != 1 {
						write(c, data)
					}
					if mode != 0 {
						read(c, buf)
						if !bytes.Equal(buf, data) {
							panic("download mismatch")
						}
					}
					if mode == 2 {
						rtt = append(rtt, time.Since(before).Nanoseconds())
					}
				}
				elapsed := time.Since(start)
				var stats [64]byte
				read(c, stats[:])
				values := make([]uint64, 8)
				for i := range values {
					values[i] = binary.BigEndian.Uint64(stats[i*8:])
				}
				if values[0] != marker || values[1] != uint64(n) || values[2] != uint64(count) || values[3] != uint64(mode) || values[7] != 1 {
					panic("stats mismatch")
				}
				median, p95 := int64(0), int64(0)
				if len(rtt) > 0 {
					slices.Sort(rtt)
					median = rtt[len(rtt)/2]
					p95 = rtt[(len(rtt)*95)/100]
				}
				must(json.NewEncoder(os.Stdout).Encode(map[string]any{
					"tls_version": tls.VersionName(version), "cipher": tls.CipherSuiteName(c.ConnectionState().CipherSuite),
					"mode": []string{"open", "seal", "ping"}[mode], "bytes": n, "calls": count,
					"server_user_ns": values[4], "server_sys_ns": values[5], "server_wall_ns": values[6],
					"client_wall_ns": elapsed.Nanoseconds(), "rtt_median_ns": median, "rtt_p95_ns": p95, "payload_ok": true}))
			}
		}
		write(c, make([]byte, 12))
		must(c.Close())
	}
}
func main() {
	listen := flag.String("listen", "", "test server listen address")
	connect := flag.String("connect", "", "test server address")
	cert := flag.String("cert", "", "DER certificate received through SSH")
	flag.Parse()
	if *focused {
		sizes = []int{5, 128, 1400, 16384}
	}
	if *listen != "" && *connect == "" {
		serve(*listen)
	} else if *connect != "" && *listen == "" && *cert != "" {
		client(*connect, *cert)
	} else {
		fmt.Fprintln(os.Stderr, "use -listen IP:port or -connect IP:port -cert FILE")
		os.Exit(2)
	}
}
