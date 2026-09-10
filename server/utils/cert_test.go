package utils

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestFirstBootCertificateAndReuse(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "tls", "server.crt")
	key := filepath.Join(dir, "tls", "server.key")
	if err := ensureGeneratedCertificate(cert, key); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(key)
	info, _ := os.Stat(key)
	if info.Mode().Perm() != 0600 {
		t.Fatal("private key permissions", info.Mode())
	}
	if err := ensureGeneratedCertificate(cert, key); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(key)
	if !bytes.Equal(before, after) {
		t.Fatal("certificate rotated on restart")
	}
	second := t.TempDir()
	if err := ensureGeneratedCertificate(filepath.Join(second, "cert"), filepath.Join(second, "key")); err != nil {
		t.Fatal(err)
	}
	other, _ := os.ReadFile(filepath.Join(second, "key"))
	if bytes.Equal(before, other) {
		t.Fatal("shared device key")
	}
}
func TestExistingInvalidAndCustomCertificatesArePreserved(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert")
	key := filepath.Join(dir, "key")
	if err := EnsureServerCertificate(cert, key); err == nil {
		t.Fatal("missing custom certificate accepted")
	}
	if _, err := os.Stat(cert); !os.IsNotExist(err) {
		t.Fatal("created custom certificate")
	}
	os.WriteFile(cert, []byte("existing certificate"), 0600)
	os.WriteFile(key, []byte("existing key"), 0600)
	if err := ensureGeneratedCertificate(cert, key); err == nil {
		t.Fatal("invalid pair accepted")
	}
	after, _ := os.ReadFile(key)
	if string(after) != "existing key" {
		t.Fatal("overwrote existing key")
	}
}
