package vm

import (
	"errors"
	"strings"
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

func TestValidHostnameMatchesDHCPRule(t *testing.T) {
	for _, ok := range []string{"nanokvm", "kvm-01", "kvm.lab.example", "a"} {
		if err := validHostname(ok); err != nil {
			t.Errorf("rejected %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-kvm", "kvm-", "kvm lab", "kvm/1", "kvm&x", "a..b", ".kvm", "kvm.", strings.Repeat("a", 64)} {
		if err := validHostname(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestReplaceHostEntryOnlyRenamesWholeNames(t *testing.T) {
	hosts := "127.0.0.1\tlocalhost local\n# local comment\n127.0.1.1 local local.lan\n"
	got := replaceHostEntry(hosts, "local", "kvm")
	want := "127.0.0.1\tlocalhost\tkvm\n# local comment\n127.0.1.1\tkvm\tlocal.lan\n"
	if got != want {
		t.Fatalf("hosts =\n%q\nwant\n%q", got, want)
	}
}
