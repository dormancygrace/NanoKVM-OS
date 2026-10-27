package bmc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/pkg/bmc"
)

type fakeATX struct{}

func (fakeATX) State(context.Context) (bmc.ATXState, error) {
	return bmc.ATXState{PowerButton: true, ResetButton: true, PowerLED: true, PowerOn: true}, nil
}
func (fakeATX) Press(context.Context, string, int) error { return nil }

// openrc plays OpenRC for the IPMI service: its state, its runlevel link,
// and a stop that can fail.
type openrc struct {
	link      string
	running   bool
	stopFails bool
	calls     []string
}

func (o *openrc) run(name string, args ...string) error {
	call := strings.Join(append([]string{name}, args...), " ")
	if status := " " + bmc.IPMIService + " status"; strings.HasSuffix(call, status) {
		// Status is not a change: leave it out of the calls.
		if o.running {
			return nil
		}
		return errors.New("stopped")
	}
	o.calls = append(o.calls, call)
	switch {
	case strings.HasSuffix(call, " restart"):
		o.running = true
	case strings.HasSuffix(call, " stop"):
		if o.stopFails {
			return errors.New("stop failed")
		}
		o.running = false
	case strings.HasPrefix(call, "rc-update add"):
		return os.WriteFile(o.link, nil, 0o644)
	case strings.HasPrefix(call, "rc-update del"):
		return os.Remove(o.link)
	}
	return nil
}

func newTestService(t *testing.T, ipmiInstalled bool) (*Service, *openrc) {
	t.Helper()
	dir := t.TempDir()
	daemon := filepath.Join(dir, "nkos-ipmi")
	if ipmiInstalled {
		if err := os.WriteFile(daemon, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rc := &openrc{link: filepath.Join(dir, "runlevel-link")}
	s := &Service{
		configPath:   filepath.Join(dir, "bmc/config.json"),
		daemonPath:   daemon,
		runlevelLink: rc.link,
		atx:          fakeATX{},
		run:          rc.run,
		install:      func() error { rc.calls = append(rc.calls, "apk add"); return nil },
		remove:       func() error { rc.calls = append(rc.calls, "apk del"); return nil },
	}
	return s, rc
}

func redfishCode(s *Service, user, password string) int {
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems/1", nil)
	req.SetBasicAuth(user, password)
	rec := httptest.NewRecorder()
	s.ServeRedfish(rec, req)
	return rec.Code
}

func TestRedfishFollowsTheSettings(t *testing.T) {
	s, rc := newTestService(t, false)
	if code := redfishCode(s, "admin", "secret-123"); code != http.StatusNotFound {
		t.Fatalf("Redfish off answered %d", code)
	}
	settings := Settings{Redfish: true, Username: "admin", Password: "secret-123", IPMIPort: 623, HoldSeconds: 5}
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	if code := redfishCode(s, "admin", "secret-123"); code != http.StatusOK {
		t.Fatalf("Redfish on answered %d", code)
	}
	if len(rc.calls) != 0 {
		t.Fatalf("Redfish alone touched a service: %v", rc.calls)
	}
	// An empty password keeps the stored one.
	settings.Password = ""
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	if code := redfishCode(s, "admin", "secret-123"); code != http.StatusOK {
		t.Fatalf("stored password lost: %d", code)
	}
	info, _ := os.Stat(s.configPath)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v", info.Mode())
	}
	settings.Redfish = false
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	if code := redfishCode(s, "admin", "secret-123"); code != http.StatusNotFound {
		t.Fatalf("Redfish switched off answered %d", code)
	}
}

// Without the add-on the page has no port field and sends no port.
func TestRedfishWithoutTheAddOnKeepsThePort(t *testing.T) {
	s, _ := newTestService(t, false)
	if err := s.Configure(Settings{Redfish: true, Username: "admin", Password: "secret-123"}); err != nil {
		t.Fatal(err)
	}
	if config, _ := bmc.Load(s.configPath); config.IPMIPort != bmc.DefaultIPMIPort {
		t.Fatalf("port %d", config.IPMIPort)
	}
}

func TestIPMIStartsAndStops(t *testing.T) {
	s, _ := newTestService(t, false)
	settings := Settings{IPMI: true, Username: "admin", Password: "secret-123", IPMIPort: 623, HoldSeconds: 5}
	if err := s.Configure(settings); err == nil {
		t.Fatal("IPMI turned on without the add-on")
	}
	s, rc := newTestService(t, true)
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rc.calls, "\n")
	if !strings.Contains(joined, "rc-update add nanokvm-ipmi default") || !strings.Contains(joined, "nanokvm-ipmi restart") {
		t.Fatalf("not started: %v", rc.calls)
	}
	status, _ := s.Status()
	if !status.PasswordSet || !status.IPMIInstalled || !status.IPMI || !status.IPMIRunning {
		t.Fatalf("status %+v", status)
	}
	rc.calls = nil
	settings.IPMI = false
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rc.calls, "\n") != "rc-service nanokvm-ipmi stop\nrc-update del nanokvm-ipmi default" {
		t.Fatalf("not stopped: %v", rc.calls)
	}
	// Off again: already stopped and out of the runlevel, nothing fails.
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
}

// The daemon keeps its startup settings: a failed stop leaves IPMI serving,
// and saying it is off would be wrong.
func TestAFailedStopIsReported(t *testing.T) {
	s, rc := newTestService(t, true)
	settings := Settings{IPMI: true, Username: "admin", Password: "secret-123", IPMIPort: 623}
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	rc.stopFails = true
	settings.IPMI = false
	if err := s.Configure(settings); err == nil {
		t.Fatal("a failed stop reported success")
	}
	if status, _ := s.Status(); !status.IPMIRunning {
		t.Fatalf("status %+v", status)
	}
	rc.calls = nil
	if err := s.UninstallIPMI(); err == nil || strings.Contains(strings.Join(rc.calls, ","), "apk del") {
		t.Fatalf("removed a running daemon's package: %v %v", err, rc.calls)
	}
}

func TestAPasswordIsNeeded(t *testing.T) {
	s, rc := newTestService(t, true)
	if err := s.Configure(Settings{Redfish: true, Username: "admin", IPMIPort: 623, HoldSeconds: 5}); err == nil {
		t.Fatal("enabled without a password")
	}
	if len(rc.calls) != 0 || s.redfish.Load() != nil {
		t.Fatalf("applied an invalid configuration: %v", rc.calls)
	}
}

func TestInstallAndUninstallIPMI(t *testing.T) {
	s, rc := newTestService(t, false)
	if err := s.InstallIPMI(); err != nil || strings.Join(rc.calls, ",") != "apk add" {
		t.Fatalf("install: %v %v", err, rc.calls)
	}
	s, rc = newTestService(t, true)
	_ = s.Configure(Settings{IPMI: true, Username: "admin", Password: "secret-123", IPMIPort: 623, HoldSeconds: 5})
	rc.calls = nil
	if err := s.UninstallIPMI(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rc.calls, ","); got != "rc-service nanokvm-ipmi stop,rc-update del nanokvm-ipmi default,apk del" {
		t.Fatalf("uninstall: %v", got)
	}
	if config, _ := bmc.Load(s.configPath); config.IPMI {
		t.Fatal("IPMI still on after removal")
	}
}

func TestRedfishWithoutAddOnPreservesCustomPort(t *testing.T) {
	s, _ := newTestService(t, false)
	config := bmc.Default()
	config.IPMIPort = 1623
	if err := bmc.Save(s.configPath, config); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(Settings{Redfish: true, Username: "admin", Password: "secret-123"}); err != nil {
		t.Fatal(err)
	}
	got, err := bmc.Load(s.configPath)
	if err != nil || got.IPMIPort != 1623 {
		t.Fatalf("port=%d err=%v", got.IPMIPort, err)
	}
}

// The power LED setting is stored when sent, kept when absent (an older
// page), and shown in the status.
func TestPowerLEDSetting(t *testing.T) {
	s, _ := newTestService(t, false)
	settings := Settings{Redfish: true, Username: "admin", Password: "secret-123", IPMIPort: 623}
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.Status(); !status.PowerLEDConnected {
		t.Fatal("the LED is not connected by default")
	}
	disconnected := false
	settings.PowerLEDConnected = &disconnected
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	settings.PowerLEDConnected = nil
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.Status(); status.PowerLEDConnected {
		t.Fatal("a request without the field changed the setting")
	}
	if bmc.PowerLEDConnected(s.configPath) {
		t.Fatal("the ATX state would still trust the LED")
	}
}
