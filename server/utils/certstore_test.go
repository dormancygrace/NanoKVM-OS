package utils

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testPair returns a self-signed pair in PEM for name, valid for a year.
func testPair(t *testing.T, name string, notBefore time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    notBefore,
		NotAfter:     notBefore.Add(365 * 24 * time.Hour),
		DNSNames:     []string{name},
		IPAddresses:  []net.IP{net.ParseIP("10.0.0.2")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func writePair(t *testing.T, dir, name string, certPEM, keyPEM []byte) (certFile, keyFile string) {
	t.Helper()
	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func servedName(t *testing.T, addr string, nextProtos ...string) (string, string) {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, NextProtos: nextProtos})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	state := conn.ConnectionState()
	return state.PeerCertificates[0].Subject.CommonName, state.NegotiatedProtocol
}

func TestParseCertificatePairAcceptsSeparateAndCombinedFiles(t *testing.T) {
	certPEM, keyPEM := testPair(t, "kvm.example", time.Now().Add(-time.Hour))
	cert, certOut, keyOut, err := ParseCertificatePair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Leaf == nil || cert.Leaf.Subject.CommonName != "kvm.example" {
		t.Fatalf("leaf not parsed: %+v", cert.Leaf)
	}
	if string(certOut) != string(certPEM) || string(keyOut) != string(keyPEM) {
		t.Fatal("clean inputs were rewritten")
	}

	combined := append(append([]byte("# comment before\n"), keyPEM...), certPEM...)
	_, certOut, keyOut, err = ParseCertificatePair(combined, combined)
	if err != nil {
		t.Fatalf("combined PEM: %v", err)
	}
	if strings.Contains(string(certOut), "PRIVATE KEY") {
		t.Fatal("the certificate output holds the key")
	}
	if strings.Contains(string(keyOut), "CERTIFICATE") || !strings.Contains(string(keyOut), "PRIVATE KEY") {
		t.Fatalf("the key output is not just the key:\n%s", keyOut)
	}
}

func TestParseCertificatePairKeepsTheChainAndBoundsIt(t *testing.T) {
	certPEM, keyPEM := testPair(t, "leaf", time.Now().Add(-time.Hour))
	issuerPEM, _ := testPair(t, "issuer", time.Now().Add(-time.Hour))
	cert, certOut, _, err := ParseCertificatePair(append(certPEM, issuerPEM...), keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.Certificate) != 2 || strings.Count(string(certOut), "BEGIN CERTIFICATE") != 2 {
		t.Fatalf("chain not kept: %d certificates", len(cert.Certificate))
	}
	long := certPEM
	for i := 0; i < maxChainLength; i++ {
		long = append(long, issuerPEM...)
	}
	if _, _, _, err := ParseCertificatePair(long, keyPEM); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("long chain accepted: %v", err)
	}
}

func TestParseCertificatePairRejectsBadInput(t *testing.T) {
	certPEM, keyPEM := testPair(t, "a", time.Now().Add(-time.Hour))
	_, otherKey := testPair(t, "b", time.Now().Add(-time.Hour))
	encrypted := "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIB\n-----END ENCRYPTED PRIVATE KEY-----\n"
	legacyEncrypted := "-----BEGIN EC PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-256-CBC,00\n\nMIIB\n-----END EC PRIVATE KEY-----\n"
	cases := []struct {
		name      string
		cert, key []byte
		want      string
	}{
		{"mismatch", certPEM, otherKey, "does not match"},
		{"no certificate", keyPEM, keyPEM, "no certificate"},
		{"no key", certPEM, certPEM, "no private key"},
		{"encrypted", certPEM, []byte(encrypted), "password-protected"},
		{"legacy encrypted", certPEM, []byte(legacyEncrypted), "password-protected"},
		{"garbage", []byte("hello"), keyPEM, "no certificate"},
		{"empty", nil, nil, "no certificate"},
	}
	for _, tc := range cases {
		_, _, _, err := ParseCertificatePair(tc.cert, tc.key)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestCertificateStoreSwapsPairsWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	first, firstKey := testPair(t, "first", time.Now().Add(-time.Hour))
	second, secondKey := testPair(t, "second", time.Now().Add(-time.Hour))
	certFile, keyFile := writePair(t, dir, "pair", first, firstKey)

	store := &CertificateStore{}
	if _, err := store.GetCertificate(nil); err == nil {
		t.Fatal("empty store served a certificate")
	}
	if err := store.Load(certFile, keyFile); err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{GetCertificate: store.GetCertificate})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			conn.Close()
		}
	}()

	if name, _ := servedName(t, ln.Addr().String()); name != "first" {
		t.Fatalf("served %q, want first", name)
	}
	// The renewed pair replaces the files in place.
	writePair(t, dir, "pair", second, secondKey)
	if err := store.Load(certFile, keyFile); err != nil {
		t.Fatal(err)
	}
	if name, _ := servedName(t, ln.Addr().String()); name != "second" {
		t.Fatalf("served %q, want second", name)
	}
	// A broken pair on disk keeps the served one and records the error.
	if err := os.WriteFile(keyFile, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Load(certFile, keyFile); err == nil {
		t.Fatal("garbage key loaded")
	}
	if name, _ := servedName(t, ln.Addr().String()); name != "second" {
		t.Fatalf("served %q after a failed reload, want second", name)
	}
	current := store.Current()
	if current.Err == nil || current.Cert == nil || current.Cert.Leaf.Subject.CommonName != "second" {
		t.Fatalf("snapshot after a failed reload: err=%v cert=%v", current.Err, current.Cert)
	}
	writePair(t, dir, "pair", second, secondKey)
	if err := store.Load(certFile, keyFile); err != nil {
		t.Fatal(err)
	}
	if current := store.Current(); current.Err != nil {
		t.Fatalf("error kept after a good reload: %v", current.Err)
	}
}

func TestPrepareServerCertificateFallsBackToTheGeneratedPair(t *testing.T) {
	dir := t.TempDir()
	origCert, origKey := defaultCertFile, defaultKeyFile
	defaultCertFile, defaultKeyFile = filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	defer func() { defaultCertFile, defaultKeyFile = origCert, origKey }()
	ServerCertificate = &CertificateStore{}
	defer func() { ServerCertificate = &CertificateStore{} }()

	// First boot: nothing on disk, the pair is generated.
	if err := PrepareServerCertificate(defaultCertFile, defaultKeyFile); err != nil {
		t.Fatal(err)
	}
	if current := ServerCertificate.Current(); current.Cert == nil || current.FallbackErr != nil || current.CertFile != defaultCertFile {
		t.Fatalf("generated pair not served: %+v", current)
	}

	// A custom pair that cannot be read: the generated one stands in, the
	// error is kept for the settings page.
	customCert, customKey := filepath.Join(dir, "custom.crt"), filepath.Join(dir, "custom.key")
	if err := PrepareServerCertificate(customCert, customKey); err != nil {
		t.Fatal(err)
	}
	current := ServerCertificate.Current()
	if current.FallbackErr == nil || !strings.Contains(current.FallbackErr.Error(), "custom.crt") || current.CertFile != defaultCertFile {
		t.Fatalf("fallback not recorded: %+v", current)
	}

	// A good custom pair is served and clears the fallback.
	certPEM, keyPEM := testPair(t, "custom", time.Now().Add(-time.Hour))
	writePair(t, dir, "custom", certPEM, keyPEM)
	if err := PrepareServerCertificate(customCert, customKey); err != nil {
		t.Fatal(err)
	}
	current = ServerCertificate.Current()
	if current.FallbackErr != nil || current.CertFile != customCert || current.Cert.Leaf.Subject.CommonName != "custom" {
		t.Fatalf("custom pair not served: %+v", current)
	}
}
