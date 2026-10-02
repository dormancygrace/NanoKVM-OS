package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUpdateTLSKeepsOtherSettings(t *testing.T) {
	original := configurationFile
	configurationFile = filepath.Join(t.TempDir(), "server.yaml")
	defer func() { configurationFile = original }()

	existing := `proto: http
port:
    http: 80
    https: 443
# kept comment
authentication: enable
security:
    loginLockoutDuration: 0
turn:
    turnUser: alice
customKey: kept
`
	if err := os.WriteFile(configurationFile, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpdateTLS("https", Cert{Crt: "/etc/kvm/server.crt", Key: "/etc/kvm/server.key"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configurationFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# kept comment", "customKey: kept", "turnUser: alice", "loginLockoutDuration: 0"} {
		if !strings.Contains(text, want) {
			t.Errorf("lost %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "trustedProxies") {
		t.Errorf("wrote an explicit trustedProxies list, disabling the loopback default:\n%s", text)
	}
	var conf Config
	if err = yaml.Unmarshal(data, &conf); err != nil {
		t.Fatal(err)
	}
	if conf.Proto != "https" || conf.Cert.Crt != "/etc/kvm/server.crt" || conf.Cert.Key != "/etc/kvm/server.key" || conf.Port.Https != 443 {
		t.Fatalf("parsed config = %+v", conf)
	}
	if info, err := os.Stat(configurationFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}
}

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
