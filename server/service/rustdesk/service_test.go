package rustdesk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestRemoteAccessPreparationFailurePreservesSettingsAndRunningService(t *testing.T) {
	temporaryConfig(t)
	current := defaultConfig()
	current.Password = "stored-pass"
	if err := writeConfig(current); err != nil {
		t.Fatal(err)
	}
	s := NewService(NewBridge())
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "apk" {
			t.Fatal("service changed before USB preparation succeeded")
		}
		return nil, nil
	}
	called := false
	s.ensureUSB = func(audio bool) error {
		called = true
		if !audio {
			t.Error("sound must default to enabled")
		}
		return errors.New("endpoint budget exceeded")
	}
	candidate := current
	candidate.Enabled = true
	if err := s.Configure(candidate); err == nil || !strings.Contains(err.Error(), "USB") {
		t.Fatalf("error: %v", err)
	}
	if !called {
		t.Fatal("enabled remote access did not prepare USB")
	}
	got, err := readConfig()
	if err != nil || got.Enabled {
		t.Fatal("failed preparation changed configuration")
	}
}

func TestRestartsAndUnrelatedSavesKeepUserUSBComposition(t *testing.T) {
	temporaryConfig(t)
	current := defaultConfig()
	current.Enabled = true
	current.Password = "stored-pass"
	if err := writeConfig(current); err != nil {
		t.Fatal(err)
	}
	media, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	b := NewBridge()
	b.media = media // already started: Configure must not open runtime sockets
	s := NewService(b)
	s.run = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	calls := 0
	s.ensureUSB = func(bool) error { calls++; return nil }

	// The daemon asks on every start, e.g. after boot or a package upgrade.
	_ = s.PrepareUSB()
	candidate := current
	candidate.MaxClients = 2
	if err := s.Configure(candidate); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("USB composition changed %d times without turning RustDesk on", calls)
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

func TestRustDeskVersionComesFromInstalledDaemonMetadata(t *testing.T) {
	temporaryConfig(t)
	for _, tc := range []struct {
		name, data, want string
		installed        bool
	}{
		{"current base", `{"rustdesk_version":"1.4.9"}`, "1.4.9", true},
		{"future base", `{"rustdesk_version":"1.5.0"}`, "1.5.0", true},
		{"invalid metadata", `invalid`, "", true},
		{"missing field", `{}`, "", true},
		{"invalid version", `{"rustdesk_version":"unexpected text"}`, "", true},
		{"uninstalled stale metadata", `{"rustdesk_version":"1.4.9"}`, "", false},
		{"old package without metadata", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(NewBridge())
			s.upstreamFile = filepath.Join(t.TempDir(), "upstream.json")
			if tc.data != "" {
				if err := os.WriteFile(s.upstreamFile, []byte(tc.data), 0644); err != nil {
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
			if err != nil || got.RustDeskVersion != tc.want {
				t.Fatalf("got %q, want %q, error %v", got.RustDeskVersion, tc.want, err)
			}
		})
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

func TestTransportConfigIsOptionalAndCapabilityGated(t *testing.T) {
	temporaryConfig(t)
	s := NewService(NewBridge())
	s.upstreamFile = filepath.Join(t.TempDir(), "upstream.json")
	if defaultConfig().WebRTC || s.supportsTransportSettings() {
		t.Fatal("WebRTC must be opt-in")
	}
	for _, m := range []string{`{"rustdesk_version":"1.5.0"}`, `{}`, `broken`} {
		if err := os.WriteFile(s.upstreamFile, []byte(m), 0600); err != nil {
			t.Fatal(err)
		}
		if s.supportsTransportSettings() {
			t.Fatal("legacy metadata must not expose transport controls")
		}
	}
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "apk" && args[0] == "info" {
			return []byte(Package + "-0.3.0-r1"), nil
		}
		t.Fatalf("must reject unsupported option before service mutation: %s", name)
		return nil, nil
	}
	c := defaultConfig()
	c.WebRTC = true
	if err := s.Configure(c); err == nil || !strings.Contains(err.Error(), "update") {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.upstreamFile, []byte(`{"rustdesk_version":"1.5.0","features":{"transport_settings":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !s.supportsTransportSettings() {
		t.Fatal("transport capability missing")
	}
	encoded, _ := json.Marshal(defaultConfig())
	if strings.Contains(string(encoded), "webrtc_enabled") {
		t.Fatal("false option must be omitted for old daemon compatibility")
	}
}

func TestAudioCapabilityFollowsInstalledMetadataAndOptionalUSBState(t *testing.T) {
	temporaryConfig(t)
	b := NewBridge()
	usbEnabled := false
	b.audioEnabled = func() bool { return usbEnabled }
	s := NewService(b)
	s.upstreamFile = filepath.Join(t.TempDir(), "upstream.json")
	installed := true
	s.apkStamp = func() string { return fmt.Sprint(installed) }
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "apk" && strings.Join(args, " ") == "info -e "+Package && installed {
			return nil, nil
		}
		return nil, errors.New("not available or stopped")
	}
	for _, tc := range []struct {
		metadata                         string
		usb, installed, support, enabled bool
	}{
		{`{"rustdesk_version":"1.5.0","features":{"audio":true}}`, false, true, true, false},
		{`{"rustdesk_version":"1.5.0","features":{"audio":true}}`, true, true, true, true},
		{`{"rustdesk_version":"1.5.0"}`, true, true, false, false},
		{`{"rustdesk_version":"1.5.0","features":{"audio":true}}`, true, false, false, false},
		{`invalid`, true, true, false, false},
	} {
		usbEnabled, installed = tc.usb, tc.installed
		if err := os.WriteFile(s.upstreamFile, []byte(tc.metadata), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := s.Status()
		if err != nil || got.SupportsAudio != tc.support || got.USBAudioEnabled != tc.enabled {
			t.Fatalf("audio status: %+v %v", got, err)
		}
	}
}

// fakeAPK answers status probes for an installed package and records them.
type fakeAPK struct {
	mu       sync.Mutex
	stamp    int
	version  string
	update   string
	fail     bool
	gate     chan struct{} // blocks repository queries while set
	commands []string
}

func (f *fakeAPK) install(s *Service) {
	s.apkStamp = func() string { f.mu.Lock(); defer f.mu.Unlock(); return fmt.Sprint(f.stamp) }
	s.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		f.mu.Lock()
		f.commands = append(f.commands, command)
		version, update, fail, gate := f.version, f.update, f.fail, f.gate
		f.mu.Unlock()
		switch {
		case command == "apk info -e -v "+Package:
			return []byte(Package + "-" + version + "\n"), nil
		case strings.HasPrefix(command, "apk query"):
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 30*time.Second {
				return nil, errors.New("repository query cannot finish on the device")
			}
			if gate != nil {
				<-gate
			}
			if fail {
				return nil, errors.New("query failed")
			}
			if strings.Contains(command, "--upgradable") {
				if update == "" {
					return []byte("[]"), nil
				}
				return []byte(`[{"name":"nanokvm-rustdesk","version":"` + update + `"}]`), nil
			}
			return []byte(`[{"name":"nanokvm-rustdesk","version":"0.2.1-r0"}]`), nil
		}
		return nil, nil
	}
}
func waitForRefresh(s *Service) {
	for {
		s.factsMu.Lock()
		done := s.refreshing
		s.factsMu.Unlock()
		if done == nil {
			return
		}
		<-done
	}
}
func (f *fakeAPK) apkCommands() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, command := range f.commands {
		if strings.HasPrefix(command, "apk ") {
			n++
		}
	}
	f.commands = nil
	return n
}

func TestStatusReusesPackageFactsUntilAPKChanges(t *testing.T) {
	temporaryConfig(t)
	s := NewService(NewBridge())
	f := &fakeAPK{version: "0.2.0-r0", update: "0.2.1-r0"}
	f.install(s)
	check := func(version, update string, apk int) {
		t.Helper()
		status, err := s.Status()
		if err != nil || !status.Installed || !status.Available || !status.Running || status.Version != version || status.UpdateVersion != update {
			t.Fatalf("status: %+v %v", status, err)
		}
		if got := f.apkCommands(); got != apk {
			t.Fatalf("%d apk commands, want %d", got, apk)
		}
	}
	check("0.2.0-r0", "0.2.1-r0", 4)
	check("0.2.0-r0", "0.2.1-r0", 0)
	f.mu.Lock()
	f.stamp++ // for example apk update in Software settings
	f.mu.Unlock()
	check("0.2.0-r0", "0.2.1-r0", 4)
	// Package actions are noticed even when apk's files look unchanged.
	if err := s.Action("upgrade"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.version, f.update = "0.2.1-r0", ""
	f.mu.Unlock()
	f.apkCommands()
	check("0.2.1-r0", "", 4)
	check("0.2.1-r0", "", 0)
}

func TestSlowRepositoryQueryDoesNotDelayStatus(t *testing.T) {
	temporaryConfig(t)
	s := NewService(NewBridge())
	s.repositoryWait = 10 * time.Millisecond
	f := &fakeAPK{version: "0.2.0-r0", update: "0.2.1-r0", gate: make(chan struct{})}
	f.install(s)
	started := time.Now()
	status, err := s.Status()
	if err != nil || !status.Installed || status.Version != "0.2.0-r0" || status.Available || status.UpdateVersion != "" || time.Since(started) > time.Second {
		t.Fatalf("status while the indexes load: %+v %v", status, err)
	}
	// Later polls do not wait for the refresh another request started.
	s.repositoryWait = time.Minute
	started = time.Now()
	if status, err = s.Status(); err != nil || status.Available || time.Since(started) > time.Second {
		t.Fatalf("second status while the indexes load: %+v %v", status, err)
	}
	close(f.gate)
	waitForRefresh(s)
	if status, err = s.Status(); err != nil || !status.Available || status.UpdateVersion != "0.2.1-r0" {
		t.Fatalf("status after the indexes loaded: %+v %v", status, err)
	}
	// After an upgrade the previous candidate no longer applies; until the
	// indexes are read again only the availability is reused.
	f.mu.Lock()
	f.version, f.update, f.gate = "0.2.1-r0", "", make(chan struct{})
	f.stamp++
	f.mu.Unlock()
	s.repositoryWait = 10 * time.Millisecond
	if status, err = s.Status(); err != nil || status.Version != "0.2.1-r0" || !status.Available || status.UpdateVersion != "" {
		t.Fatalf("status after upgrade: %+v %v", status, err)
	}
	close(f.gate)
}

func TestFailedRepositoryQueryIsRetriedLater(t *testing.T) {
	temporaryConfig(t)
	s := NewService(NewBridge())
	f := &fakeAPK{version: "0.2.0-r0", update: "0.2.1-r0", fail: true}
	f.install(s)
	for range 2 {
		if status, err := s.Status(); err != nil || status.Available || status.UpdateVersion != "" {
			t.Fatalf("failed query: %+v %v", status, err)
		}
	}
	if got := f.apkCommands(); got != 4 {
		t.Fatalf("failed query repeated immediately: %d apk commands", got)
	}
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	s.factsMu.Lock()
	s.retryAt = time.Now().Add(-time.Second)
	s.factsMu.Unlock()
	if status, err := s.Status(); err != nil || !status.Available || status.UpdateVersion != "0.2.1-r0" {
		t.Fatalf("retried query: %+v %v", status, err)
	}
}

func TestRepositoryQueriesNeverOverlapPackageTransactions(t *testing.T) {
	temporaryConfig(t)
	s := NewService(NewBridge())
	s.repositoryWait = 0
	s.apkStamp = func() string { return "unchanged" }
	var active, overlaps, queries int
	var mu sync.Mutex
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "apk" || args[0] == "info" {
			return nil, nil
		}
		mu.Lock()
		active++
		overlaps += active - 1
		if args[0] == "query" {
			queries++
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return []byte("[]"), nil
	}
	if _, err := s.Status(); err != nil {
		t.Fatal(err)
	}
	if err := s.Action("upgrade"); err != nil {
		t.Fatal(err)
	}
	waitForRefresh(s)
	s.repositoryWait = time.Minute
	if _, err := s.Status(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if overlaps != 0 || queries == 0 {
		t.Fatalf("%d overlapping apk runs, %d queries", overlaps, queries)
	}
}

func TestStoppedServiceStatusNeedsNoRCService(t *testing.T) {
	temporaryConfig(t)
	s := NewService(NewBridge())
	f := &fakeAPK{version: "0.2.0-r0"}
	f.install(s)
	s.startedFile = filepath.Join(t.TempDir(), Package)
	rcService := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		n := 0
		for _, command := range f.commands {
			if strings.HasPrefix(command, "rc-service ") {
				n++
			}
		}
		f.commands = nil
		return n
	}
	if status, err := s.Status(); err != nil || status.Running || rcService() != 0 {
		t.Fatalf("stopped service: %+v %v", status, err)
	}
	// A started service may have crashed; openrc decides.
	if err := os.Symlink("/etc/init.d/"+Package, s.startedFile); err != nil {
		t.Fatal(err)
	}
	if status, err := s.Status(); err != nil || !status.Running || rcService() != 1 {
		t.Fatalf("started service: %+v %v", status, err)
	}
}

func TestRepositoryQueriesRunAtIdlePriority(t *testing.T) {
	// The shell reports its own nice value after runCommand lowered it.
	data, err := runCommand(withIdlePriority(context.Background()), "sh", "-c", `sleep 0.2; cut -d" " -f19 /proc/$$/stat`)
	if err != nil || strings.TrimSpace(string(data)) != "19" {
		t.Fatalf("nice %q %v", data, err)
	}
}

func TestAPKStampFollowsStateFiles(t *testing.T) {
	dir := t.TempDir()
	installed, cache := filepath.Join(dir, "installed"), filepath.Join(dir, "cache")
	if err := os.WriteFile(installed, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cache, 0755); err != nil {
		t.Fatal(err)
	}
	paths := []string{installed, cache, filepath.Join(dir, "missing")}
	stamp := apkStamp(paths)
	if apkStamp(paths) != stamp {
		t.Fatal("unchanged state changed the stamp")
	}
	index := filepath.Join(cache, "APKINDEX.0.tar.gz")
	for _, change := range []func() error{
		func() error { return os.WriteFile(index, []byte("index"), 0644) },
		func() error { return os.Chtimes(index, time.Now(), time.Unix(1, 0)) },
		func() error { return os.WriteFile(installed, []byte("ab"), 0644) },
	} {
		if err := change(); err != nil {
			t.Fatal(err)
		}
		next := apkStamp(paths)
		if next == stamp {
			t.Fatal("apk state change kept the stamp")
		}
		stamp = next
	}
}
