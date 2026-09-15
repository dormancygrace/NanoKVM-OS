package vm

import (
	"errors"
	"testing"

	"NanoKVM-Server/config"
)

func TestEnableTlsPreservesCustomCertificatePaths(t *testing.T) {
	conf := &config.Config{Proto: "http", Cert: config.Cert{Crt: "/custom/server.crt", Key: "/custom/server.key"}}
	var ensuredCert, ensuredKey string
	written := false
	err := enableTlsConfig(conf, func(got *config.Config) error {
		written = true
		if got.Proto != "https" {
			t.Fatalf("proto = %q", got.Proto)
		}
		return nil
	}, func(cert, key string) error {
		ensuredCert, ensuredKey = cert, key
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !written || ensuredCert != "/custom/server.crt" || ensuredKey != "/custom/server.key" {
		t.Fatalf("custom pair not preserved: written=%v cert=%q key=%q", written, ensuredCert, ensuredKey)
	}
}

func TestEnableTlsRejectsInvalidCertificateWithoutWritingConfig(t *testing.T) {
	conf := &config.Config{Proto: "http", Cert: config.Cert{Crt: "/custom/server.crt", Key: "/custom/server.key"}}
	invalid := errors.New("invalid certificate")
	written := false
	err := enableTlsConfig(conf, func(*config.Config) error {
		written = true
		return nil
	}, func(string, string) error { return invalid })
	if !errors.Is(err, invalid) {
		t.Fatalf("error = %v, want invalid certificate", err)
	}
	if written {
		t.Fatal("configuration written after certificate validation failed")
	}
}

func TestEnableTlsUsesDefaultPathsWhenUnset(t *testing.T) {
	conf := &config.Config{Proto: "http"}
	err := enableTlsConfig(conf, func(*config.Config) error { return nil }, func(cert, key string) error {
		if cert != "/etc/kvm/server.crt" || key != "/etc/kvm/server.key" {
			t.Fatalf("default pair = %q %q", cert, key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
