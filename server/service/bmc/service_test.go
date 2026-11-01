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

func TestAPasswordIsNeeded(t *testing.T) {
	s, rc := newTestService(t, true)
	if err := s.Configure(Settings{Redfish: true, Username: "admin", IPMIPort: 623, HoldSeconds: 5}); err == nil {
		t.Fatal("enabled without a password")
	}
	if len(rc.calls) != 0 || s.redfish.Load() != nil {
		t.Fatalf("applied an invalid configuration: %v", rc.calls)
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
