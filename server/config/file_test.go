package config

import (
	"os"
	"path/filepath"
	"reflect"

	"testing"
)

func TestReadReportsInvalidYAMLInsteadOfExiting(t *testing.T) {
	original := configurationFile
	configurationFile = filepath.Join(t.TempDir(), "server.yaml")
	defer func() { configurationFile = original }()
	if err := os.WriteFile(configurationFile, []byte("proto: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(); err == nil {
		t.Fatal("invalid YAML accepted")
	}
	if err := UpdateTLS("http", Cert{}); err == nil {
		t.Fatal("UpdateTLS rewrote an unparsable configuration")
	}
}

// Without a common file lock, TLS saves a snapshot while the first edit
// is still running, and that first edit then silently removes TLS changes.

func TestLegacyConfigDecoding(t *testing.T) {
	previous := values
	t.Cleanup(func() { values = previous })
	for _, tc := range []struct {
		input   string
		proxies []string
	}{
		{`PORT: {HTTP: "8080", HTTPS: 443}
security: {trustedProxies: "127.0.0.1,::1"}`, []string{"127.0.0.1", "::1"}},
		{`port.http: 8080
port.https: 443
security: {trustedProxies: []}`, []string{}},
		{`defaults: &ports {http: 8080, https: 443}
port: {<<: *ports}`, nil},
	} {
		if err := readValues([]byte(tc.input)); err != nil {
			t.Fatal(err)
		}
		var got Config
		if err := decodeValues(values, &got); err != nil {
			t.Fatal(err)
		}
		if got.Port.Http != 8080 || got.Port.Https != 443 || !reflect.DeepEqual(got.Security.TrustedProxies, tc.proxies) {
			t.Fatalf("config = %#v", got)
		}
	}
}

// validate accepts the port forms the old loader did (quoted, hex, YAML
// floats) and sets aside a file whose ports the decoder would reject, rather
// than letting the full decode fail on start.
func TestValidatePortForms(t *testing.T) {
	original, previous := configurationFile, values
	t.Cleanup(func() { configurationFile, values = original, previous })
	for input, valid := range map[string]bool{
		"port: {http: 8080, https: 443}":     true,
		`port: {http: "8080", https: "443"}`: true,
		`port: {http: "0x1F90", https: 443}`: true,
		"port: {http: 8080.0, https: 443}":   true,
		"port: {http: 8080, https: 0}":       false,
		`port: {http: "web", https: 443}`:    false,
		`port: {http: "8080.0", https: 443}`: false,
		"port: {http: 8080}":                 false,
		"port: 8080":                         false,
		"proto: http":                        false,
	} {
		configurationFile = filepath.Join(t.TempDir(), "server.yaml")
		if err := os.WriteFile(configurationFile, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := readValues([]byte(input)); err != nil {
			t.Fatal(err)
		}
		if err := validate(); err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		_, err := os.Stat(configurationFile + ".bad")
		if setAside := err == nil; setAside == valid {
			t.Errorf("%s: set aside = %v, want %v", input, setAside, !valid)
		}
	}
}
