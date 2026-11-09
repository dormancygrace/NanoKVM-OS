package rustdesk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// The real update lock lives under /kvmapp; package tests hold a stand-in.
func TestMain(m *testing.M) {
	updateLock = func() (io.Closer, error) { return nopCloser{}, nil }
	os.Exit(m.Run())
}

func temporaryConfig(t *testing.T) {
	t.Helper()
	oldDir, oldFile := ConfigDir, ConfigFile
	ConfigDir = t.TempDir()
	ConfigFile = filepath.Join(ConfigDir, "config.json")
	t.Cleanup(func() { ConfigDir, ConfigFile = oldDir, oldFile })
}

func TestCustomAddressesAndConfigValidation(t *testing.T) {
	tests := []struct {
		input, expected string
		bad             bool
	}{
		{"hbbs.example", "hbbs.example:21116", false},
		{"192.0.2.1", "192.0.2.1:21116", false},
		{"2001:db8::1", "[2001:db8::1]:21116", false},
		{"[2001:db8::1]:444", "[2001:db8::1]:444", false},
		{"https://example", "", true}, {"a:0", "", true}, {"a:65536", "", true},
		{"a;touch file", "", true}, {"user@server", "", true},
	}
	for _, tt := range tests {
		got, err := serverAddress(tt.input, "21116")
		if (err != nil) != tt.bad || (!tt.bad && got != tt.expected) {
			t.Errorf("%q: %q %v", tt.input, got, err)
		}
	}
	c := defaultConfig()
	c.Password = "example-pass"
	c.Official = false
	if validateConfig(&c) == nil {
		t.Fatal("custom mode requires an ID server")
	}
	c.Rendezvous = "hbbs.example"
	c.Key = "bad"
	if validateConfig(&c) == nil {
		t.Fatal("invalid server key accepted")
	}
	c.Key = ""
	if err := validateConfig(&c); err != nil {
		t.Fatal(err)
	}
	if c.Rendezvous != "hbbs.example:21116" {
		t.Fatal(c.Rendezvous)
	}
}

func TestStatusRedactsPasswordAndDistinguishesAvailability(t *testing.T) {
	temporaryConfig(t)
	c := defaultConfig()
	c.Password = "private-pass"
	if err := writeConfig(c); err != nil {
		t.Fatal(err)
	}
	s := NewService(NewBridge())
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "apk" && args[0] == "query" {
			return []byte(`[{"name":"nanokvm-rustdesk","version":"0.1.0-r0"}]`), nil
		}
		return nil, errors.New("not installed")
	}
	status, err := s.Status()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), c.Password) || !status.HasPassword || status.Installed || !status.Available || status.Running {
		t.Fatal(string(encoded))
	}
	info, err := os.Stat(ConfigFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions: %v %v", info, err)
	}
}

func TestPackageActionsUseFixedArgv(t *testing.T) {
	s := NewService(NewBridge())
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "apk" || len(args) != 2 || args[1] != Package {
			t.Fatalf("%s %v", name, args)
		}
		return nil, nil
	}
	for _, action := range []string{"install", "remove"} {
		if err := s.Action(action); err != nil {
			t.Fatal(err)
		}
	}
	if s.Action("install another-package") == nil {
		t.Fatal("unknown action accepted")
	}
}

// A password saved by the previous add-on must keep working after upgrade.

func TestTemporaryPasswordExposedOnlyWhenSelectedAndRunning(t *testing.T) {
	temporaryConfig(t)
	c := defaultConfig()
	c.Password = "never-reveal-permanent"
	if err := writeConfig(c); err != nil {
		t.Fatal(err)
	}
	s := NewService(NewBridge())
	s.passwordFile = filepath.Join(t.TempDir(), "temporary-password")
	if err := os.WriteFile(s.passwordFile, []byte("ABCDEFGH23"), 0600); err != nil {
		t.Fatal(err)
	}
	running := true
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "rc-service" && !running {
			return nil, errors.New("stopped")
		}
		if name == "apk" && strings.Join(args, " ") == "info -e -v "+Package {
			return []byte(Package + "-0.2.0-r0\n"), nil
		}
		return nil, nil
	}
	status, err := s.Status()
	if err != nil || status.TemporaryPassword != "ABCDEFGH23" || status.Config.Password != "" || status.Version != "0.2.0-r0" {
		t.Fatalf("temporary status: %+v %v", status, err)
	}
	running = false
	status, err = s.Status()
	if err != nil || status.TemporaryPassword != "" {
		t.Fatal("stale stopped password exposed")
	}
	running = true
	c.PasswordMode = "permanent"
	if err := writeConfig(c); err != nil {
		t.Fatal(err)
	}
	status, err = s.Status()
	encoded, _ := json.Marshal(status)
	if err != nil || status.TemporaryPassword != "" || strings.Contains(string(encoded), c.Password) {
		t.Fatalf("permanent credential exposed: %s %v", encoded, err)
	}
}

// Output that is not the JSON answer says nothing about the package. It must
// fail, so that it is retried, instead of being cached as "not available".
