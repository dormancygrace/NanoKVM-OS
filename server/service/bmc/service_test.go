package bmc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/internal/bmc"
)

type fakeATX struct{}

func (fakeATX) State(context.Context) (bmc.ATXState, error) {
	return bmc.ATXState{PowerButton: true, ResetButton: true, PowerLED: true, PowerOn: true}, nil
}
func (fakeATX) Press(context.Context, string, int) error { return nil }

func newTestService(t *testing.T, ipmiInstalled bool) (*Service, *[]string) {
	t.Helper()
	dir := t.TempDir()
	daemon := filepath.Join(dir, "nkos-ipmi")
	if ipmiInstalled {
		if err := os.WriteFile(daemon, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	s := &Service{
		configPath: filepath.Join(dir, "bmc/config.json"),
		daemonPath: daemon,
		atx:        fakeATX{},
		run: func(name string, args ...string) error {
			calls = append(calls, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
		install: func() error { calls = append(calls, "apk add"); return nil },
		remove:  func() error { calls = append(calls, "apk del"); return nil },
	}
	return s, &calls
}

func redfishCode(s *Service, user, password string) int {
	req := httptest.NewRequest(http.MethodGet, "/redfish/v1/Systems/1", nil)
	req.SetBasicAuth(user, password)
	rec := httptest.NewRecorder()
	s.ServeRedfish(rec, req)
	return rec.Code
}

func TestRedfishFollowsTheSettings(t *testing.T) {
	s, calls := newTestService(t, false)
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
	if len(*calls) != 0 {
		t.Fatalf("Redfish alone touched a service: %v", *calls)
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

func TestIPMIStartsAndStops(t *testing.T) {
	s, calls := newTestService(t, false)
	settings := Settings{IPMI: true, Username: "admin", Password: "secret-123", IPMIPort: 623, HoldSeconds: 5}
	if err := s.Configure(settings); err == nil {
		t.Fatal("IPMI turned on without the add-on")
	}
	s, calls = newTestService(t, true)
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "rc-update add nanokvm-ipmi default") || !strings.Contains(joined, "nanokvm-ipmi restart") {
		t.Fatalf("not started: %v", *calls)
	}
	status, _ := s.Status()
	if !status.PasswordSet || !status.IPMIInstalled || !status.IPMI {
		t.Fatalf("status %+v", status)
	}
	*calls = nil
	settings.IPMI = false
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*calls, "\n") != "rc-service nanokvm-ipmi stop\nrc-update del nanokvm-ipmi default" {
		t.Fatalf("not stopped: %v", *calls)
	}
}

func TestAPasswordIsNeeded(t *testing.T) {
	s, calls := newTestService(t, true)
	if err := s.Configure(Settings{Redfish: true, Username: "admin", IPMIPort: 623, HoldSeconds: 5}); err == nil {
		t.Fatal("enabled without a password")
	}
	if len(*calls) != 0 || s.redfish.Load() != nil {
		t.Fatalf("applied an invalid configuration: %v", *calls)
	}
}

func TestInstallAndUninstallIPMI(t *testing.T) {
	s, calls := newTestService(t, false)
	if err := s.InstallIPMI(); err != nil || strings.Join(*calls, ",") != "apk add" {
		t.Fatalf("install: %v %v", err, *calls)
	}
	s, calls = newTestService(t, true)
	_ = s.Configure(Settings{IPMI: true, Username: "admin", Password: "secret-123", IPMIPort: 623, HoldSeconds: 5})
	*calls = nil
	if err := s.UninstallIPMI(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "rc-service nanokvm-ipmi stop,rc-update del nanokvm-ipmi default,apk del" {
		t.Fatalf("uninstall: %v", got)
	}
	if config, _ := bmc.Load(s.configPath); config.IPMI {
		t.Fatal("IPMI still on after removal")
	}
}
