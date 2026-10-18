package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestConcurrentConfigEditsKeepBothUpdates(t *testing.T) {
	original := configurationFile
	configurationFile = filepath.Join(t.TempDir(), "server.yaml")
	defer func() { configurationFile = original }()
	if err := os.WriteFile(configurationFile, []byte("proto: http\nstun: old.example:3478\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		first <- editFile(func(root *yaml.Node) {
			close(entered)
			<-release
			setScalar(root, "stun", "new.example:3478")
		})
	}()
	<-entered
	go func() { second <- UpdateTLS("https", Cert{Crt: "cert.pem", Key: "key.pem"}) }()
	// Without a common file lock, TLS saves a snapshot while the first edit
	// is still running, and that first edit then silently removes TLS changes.
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.Proto != "https" || got.Cert.Crt != "cert.pem" || got.Stun != "new.example:3478" {
		t.Fatalf("lost concurrent configuration edit: %+v", got)
	}
}
