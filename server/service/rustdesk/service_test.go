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

func TestPasswordModeDefaultsAndLegacyMigration(t *testing.T) {
	temporaryConfig(t)
	fresh, err := readConfig()
	if err != nil || fresh.PasswordMode != "temporary" || fresh.Password != "" {
		t.Fatalf("fresh config: %+v %v", fresh, err)
	}
	// A password saved by the previous add-on must keep working after upgrade.
	legacy := []byte("{\"password\":\"legacy-secret\",\"codec\":\"h265\",\"max_clients\":1}")
	if err := os.WriteFile(ConfigFile, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	migrated, err := readConfig()
	if err != nil || migrated.PasswordMode != "permanent" || migrated.Password != "legacy-secret" || migrated.Codec != "auto" {
		t.Fatalf("legacy config: %+v %v", migrated, err)
	}
	fresh.PasswordMode = "permanent"
	if validateConfig(&fresh) == nil {
		t.Fatal("permanent mode accepted an empty password")
	}
	fresh.PasswordMode = "invalid"
	if validateConfig(&fresh) == nil {
		t.Fatal("unknown mode accepted")
	}
}

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

func TestRegenerationRequiresRunningTemporaryModeAndPreservesConfig(t *testing.T) {
	temporaryConfig(t)
	c := defaultConfig()
	c.Enabled = true
	c.Password = "retained-permanent"
	if err := writeConfig(c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(ConfigFile)
	s := NewService(NewBridge())
	running := true
	var commands []string
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if !running {
			return nil, errors.New("stopped")
		}
		return nil, nil
	}
	if err := s.Action("regenerate-password"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || commands[1] != "rc-service nanokvm-rustdesk restart" {
		t.Fatal(commands)
	}
	after, _ := os.ReadFile(ConfigFile)
	if string(before) != string(after) {
		t.Fatal("regeneration changed persistent credentials or config")
	}
	running = false
	if s.Action("regenerate-password") == nil {
		t.Fatal("stopped service regenerated a password")
	}
	c.PasswordMode = "permanent"
	if err := writeConfig(c); err != nil {
		t.Fatal(err)
	}
	commands = nil
	if s.Action("regenerate-password") == nil || len(commands) != 0 {
		t.Fatal("permanent mode was restarted")
	}
}

// Output that is not the JSON answer says nothing about the package. It must
// fail, so that it is retried, instead of being cached as "not available".
func TestUnreadableRepositoryQueryIsAFailure(t *testing.T) {
	s := NewService(NewBridge())
	s.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("WARNING: something\n"), nil
	}
	if _, available, err := s.repositoryVersion(false); err == nil || available {
		t.Fatalf("available %v, err %v", available, err)
	}
	s.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("[\n]\n"), nil
	}
	if _, available, err := s.repositoryVersion(false); err != nil || available {
		t.Fatalf("no match: available %v, err %v", available, err)
	}
}

func TestPublishedSourceURLUsesInstalledPackageRecord(t *testing.T) {
	temporaryConfig(t)
	for _, tc := range []struct {
		name, data, want string
		installed        bool
	}{
		{"versioned public URL", `{"url":"https://github.com/dormancygrace/NanoKVM-OS-packages/releases/download/nanokvm-rustdesk-0.2.1-r1/source.tar.gz"}`, "https://github.com/dormancygrace/NanoKVM-OS-packages/releases/download/nanokvm-rustdesk-0.2.1-r1/source.tar.gz", true},
		{"HTTP", `{"url":"http://example.com/source.tar.gz"}`, "", true},
		{"script URL", `{"url":"javascript:alert(1)"}`, "", true},
		{"userinfo", `{"url":"https://user@example.com/source.tar.gz"}`, "", true},
		{"malformed record", `invalid`, "", true},
		{"missing file", "", "", true},
		{"stale uninstalled file", `{"url":"https://example.com/source.tar.gz"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(NewBridge())
			s.sourceFile = filepath.Join(t.TempDir(), "source.json")
			if tc.data != "" {
				if err := os.WriteFile(s.sourceFile, []byte(tc.data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "apk" && strings.Join(args, " ") == "info -e "+Package && tc.installed {
					return nil, nil
				}
				return nil, errors.New("not available or stopped")
			}
			got, err := s.Status()
			if err != nil || got.SourceURL != tc.want {
				t.Fatalf("got %q, want %q, error %v", got.SourceURL, tc.want, err)
			}
		})
	}
}
