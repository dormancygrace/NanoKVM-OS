// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// tls-smoke sends random data through a TLS 1.3 loopback connection of
// crypto/tls and checks that it arrives intact with
// TLS_CHACHA20_POLY1305_SHA256, the suite Go picks without AES hardware.
// It reports the process CPU time per MB, which covers Seal on the client,
// Open on the server and the loopback TCP. Usage: tls-smoke [MB] [write size]
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"syscall"
	"time"
)

func cpuTime() (user, sys time.Duration) {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano()), time.Duration(ru.Stime.Nano())
}

func main() {
	mb, size := 32, 16384
	if len(os.Args) > 1 {
		mb, _ = strconv.Atoi(os.Args[1])
	}
	if len(os.Args) > 2 {
		size, _ = strconv.Atoi(os.Args[2])
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		panic(err)
	}
	data := make([]byte, mb<<20)
	rand.Read(data)
	match := make(chan bool)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			panic(err)
		}
		buf := make([]byte, 64<<10)
		pos, same := 0, true
		for {
			n, err := c.Read(buf)
			same = same && pos+n <= len(data) && bytes.Equal(buf[:n], data[pos:pos+n])
			pos += n
			if err == io.EOF {
				break
			} else if err != nil {
				panic(err)
			}
		}
		match <- same && pos == len(data)
	}()
	c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: "localhost", RootCAs: roots, MinVersion: tls.VersionTLS13})
	if err != nil {
		panic(err)
	}
	suite := tls.CipherSuiteName(c.ConnectionState().CipherSuite)
	u0, s0 := cpuTime()
	w0 := time.Now()
	for rest := data; len(rest) > 0; {
		n := min(size, len(rest))
		if _, err := c.Write(rest[:n]); err != nil {
			panic(err)
		}
		rest = rest[n:]
	}
	c.Close()
	same := <-match
	u1, s1 := cpuTime()
	wall := time.Since(w0)
	user, sys := u1-u0, s1-s0
	perMB := func(d time.Duration) float64 { return d.Seconds() * 1e3 / float64(mb) }
	fmt.Printf("%s %d MiB in %d B writes: data ok=%v, CPU ms/MiB user %.1f sys %.1f, %.2f MiB/s wall\n",
		suite, mb, size, same, perMB(user), perMB(sys), float64(mb)/wall.Seconds())
	if !same || suite != "TLS_CHACHA20_POLY1305_SHA256" {
		os.Exit(1)
	}
}
