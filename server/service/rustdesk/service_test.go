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
	if err != nil || migrated.PasswordMode != "permanent" || migrated.Password != "legacy-secret" {
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

func TestUpdateButtonOnlyReceivesAPKUpgradeCandidates(t *testing.T) {
	temporaryConfig(t)
	tests := []struct {
		name, available, upgrades, want string
		fail                            bool
	}{
		{"newer", "0.2.1-r0", `[{"name":"nanokvm-rustdesk","version":"0.2.1-r0"}]`, "0.2.1-r0", false},
		{"same version", "0.2.0-r0", "", "", false},
		{"older repository", "0.1.0-r0", "", "", false},
		{"no repository package", "", "", "", false},
		{"package query failed", "0.2.1-r0", "", "", true},
		{"malformed query", "0.2.1-r0", "invalid json", "", false},
		{"unrelated package", "0.2.1-r0", `[{"name":"other","version":"99-r0"}]`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewService(NewBridge())
			s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
				switch name + " " + strings.Join(args, " ") {
				case "apk info -e " + Package:
					return nil, nil
				case "apk info -e -v " + Package:
					return []byte(Package + "-0.2.0-r0\n"), nil
				case "apk query --no-network --from=repositories --format=json --fields=name,version " + Package:
					if tt.available == "" {
						return nil, nil
					}
					return []byte(`[{"name":"nanokvm-rustdesk","version":"` + tt.available + `"}]`), nil
				case "apk query --no-network --format=json --fields=name,version --upgradable " + Package:
					if tt.fail {
						return nil, errors.New("query failed")
					}
					return []byte(tt.upgrades), nil
				case "rc-service " + Package + " status":
					return nil, errors.New("stopped")
				default:
					t.Fatalf("unexpected command: %s %v", name, args)
					return nil, errors.New("unexpected command")
				}
			}
			status, err := s.Status()
			if err != nil || status.UpdateVersion != tt.want || status.Version != "0.2.0-r0" {
				t.Fatalf("status: %+v %v", status, err)
			}
		})
	}
}
