package rustdesk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		if name == "apk" && args[0] == "search" {
			return []byte("nanokvm-rustdesk-0.1.0-r0\n"), nil
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

func TestDisablePreservesPasswordWithoutAbsentRunlevelFailure(t *testing.T) {
	temporaryConfig(t)
	c := defaultConfig()
	c.Password = "stored-pass"
	if err := writeConfig(c); err != nil {
		t.Fatal(err)
	}
	var commands []string
	s := NewService(NewBridge())
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		commands = append(commands, command)
		if name == "rc-service" {
			return nil, errors.New("stopped")
		}
		if name == "rc-update" && args[0] == "del" {
			t.Fatal("must not delete an absent runlevel entry")
		}
		return []byte("other-service | default\n"), nil
	}
	c.Password = ""
	if err := s.Configure(c); err != nil {
		t.Fatal(err)
	}
	got, err := readConfig()
	if err != nil || got.Password != "stored-pass" {
		t.Fatalf("%+v %v", got, err)
	}
	if len(commands) != 3 {
		t.Fatal(commands)
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
	for _, action := range []string{"install", "upgrade", "remove"} {
		if err := s.Action(action); err != nil {
			t.Fatal(err)
		}
	}
	if s.Action("install another-package") == nil {
		t.Fatal("unknown action accepted")
	}
}
