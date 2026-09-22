package vm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKernelModulesReportsMismatch(t *testing.T) {
	old := diagnosticModulesDir
	diagnosticModulesDir = t.TempDir()
	t.Cleanup(func() { diagnosticModulesDir = old })
	if item := kernelModules("7.2.6-nanokvm-os-r1", "", false); item.State != "error" {
		t.Fatalf("missing modules = %#v, want error", item)
	}
	if err := os.Mkdir(filepath.Join(diagnosticModulesDir, "7.2.6-nanokvm-os-r1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diagnosticModulesDir, "7.2.6-nanokvm-os-r1", "modules.dep"), []byte("kernel/test.ko:\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if item := kernelModules("7.2.6-nanokvm-os-r1", "", false); item.State != "ok" {
		t.Fatalf("matching modules = %#v, want ok", item)
	}
}

func TestKernelModulesTreatsNewTargetAsPendingReboot(t *testing.T) {
	old := diagnosticModulesDir
	diagnosticModulesDir = t.TempDir()
	t.Cleanup(func() { diagnosticModulesDir = old })
	if err := os.MkdirAll(filepath.Join(diagnosticModulesDir, "new"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diagnosticModulesDir, "new", "modules.dep"), []byte("kernel/new.ko:\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if item := kernelModules("old", "new", true); item.State != "pending-reboot" {
		t.Fatalf("pending modules=%#v", item)
	}
	if item := kernelModules("old", "new", false); item.State != "error" {
		t.Fatalf("unmarked mismatch=%#v", item)
	}
	if item := kernelModules("old", "missing", true); item.State != "error" {
		t.Fatalf("missing target=%#v", item)
	}
}

func TestBootStatusTreatsPendingKernelRebootAsExpected(t *testing.T) {
	oldBoot, oldRead := diagnosticBootDir, diagnosticReadFile
	diagnosticBootDir = t.TempDir()
	diagnosticReadFile = func(path string) ([]byte, error) {
		if path == filepath.Join(diagnosticBootDir, "kernel.release") {
			return []byte("new-kernel\n"), nil
		}
		return os.ReadFile(path)
	}
	t.Cleanup(func() { diagnosticBootDir, diagnosticReadFile = oldBoot, oldRead })
	if err := os.WriteFile(filepath.Join(diagnosticBootDir, "boot.sd"), []byte("fit"), 0644); err != nil {
		t.Fatal(err)
	}
	if item := bootStatus("old-kernel", true); item.State != "pending-reboot" || !strings.Contains(item.Detail, "pending") {
		t.Fatalf("pending reboot = %#v", item)
	}
	if item := bootStatus("old-kernel", false); item.State != "error" {
		t.Fatalf("unexpected mismatch = %#v", item)
	}
}

func TestServiceStatusDistinguishesAbsentStoppedAndTimeout(t *testing.T) {
	oldDir, oldLook, oldRun := diagnosticInitDir, diagnosticLookPath, diagnosticRun
	diagnosticInitDir = t.TempDir()
	diagnosticLookPath = func(string) (string, error) { return "rc-service", nil }
	t.Cleanup(func() { diagnosticInitDir, diagnosticLookPath, diagnosticRun = oldDir, oldLook, oldRun })

	if item := serviceStatus(context.Background(), "optional-vpn", true); item.State != "absent" || !item.Optional {
		t.Fatalf("absent optional service = %#v", item)
	}
	if err := os.WriteFile(filepath.Join(diagnosticInitDir, "optional-vpn"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	diagnosticLookPath = func(string) (string, error) { return "", errors.New("missing") }
	if item := serviceStatus(context.Background(), "optional-vpn", true); item.State != "unavailable" {
		t.Fatalf("missing OpenRC command = %#v", item)
	}
	diagnosticLookPath = func(string) (string, error) { return "rc-service", nil }
	diagnosticRun = func(context.Context, int, string, ...string) ([]byte, error) {
		return nil, exec.Command("sh", "-c", "exit 3").Run()
	}
	if item := serviceStatus(context.Background(), "optional-vpn", true); item.State != "stopped" {
		t.Fatalf("stopped service = %#v", item)
	}
	diagnosticRun = func(ctx context.Context, _ int, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if item := serviceStatus(context.Background(), "optional-vpn", true); item.State != "error" || item.Detail != "OpenRC status timed out" {
		t.Fatalf("timed out service = %#v", item)
	}
}

func TestNftHookChainsAreAnonymized(t *testing.T) {
	chains, err := parseNFTHookChains([]byte(`{"nftables":[{"table":{"family":"inet","name":"filter"}},{"chain":{"family":"inet","table":"filter","name":"input","type":"filter","hook":"input","policy":"drop"}},{"chain":{"family":"inet","table":"filter","name":"ordinary"}}]}`))
	if err != nil || len(chains) != 1 || chains[0].Hook != "input" || chains[0].Policy != "drop" {
		t.Fatalf("chains = %#v, %v", chains, err)
	}
	if chains[0].Label != "chain-1" {
		t.Fatalf("chain label = %#v", chains[0])
	}
}

func TestDiagnosticsReportExcludesAdversarialValues(t *testing.T) {
	var snapshot DiagnosticsSnapshot
	snapshot.Versions.Application = "host.example Authorization: Bearer SECRET"
	snapshot.Versions.Image = "https://user:pass@host/path?api_key=QUERY"
	snapshot.Versions.Alpine = "password is multi word secret"
	snapshot.Versions.Kernel = "192.168.4.128 aa:bb:cc:dd:ee:ff"
	snapshot.Versions.SystemBase = "device-hostname"
	snapshot.Versions.Packages = []DiagnosticPackage{{Name: "host.example", Version: "SECRET", State: "installed"}}
	snapshot.Services = []DiagnosticService{{Name: "hostname", Desired: "SECRET", DiagnosticItem: DiagnosticItem{State: "running"}}}
	snapshot.USB.Selected = []string{"password", "keyboard"}
	snapshot.Firewall.HookChains = []DiagnosticHookChain{{Label: "hostname", Hook: "input", Policy: "drop"}}
	b, err := json.Marshal(diagnosticsReport(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, forbidden := range []string{"SECRET", "user:pass", "QUERY", "multi word", "192.168.4.128", "aa:bb:cc:dd:ee:ff", "device-hostname", "host.example"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("report leaked %q: %s", forbidden, text)
		}
	}
}

func TestDiagnosticRunnerKillsChildProcessGroupOnTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runDiagnosticCommand(ctx, diagnosticsOutput, "/bin/sh", "-c", "sleep 3 & wait")
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("runner err=%v elapsed=%s", err, time.Since(started))
	}
}

func TestOpenVPN2AndStalePID(t *testing.T) {
	oldLook, oldRun, oldProc, oldRead, oldDir := diagnosticOpenVPN, diagnosticOpenVPNRun, diagnosticProcDir, diagnosticReadFile, diagnosticReadDir
	root := t.TempDir()
	diagnosticOpenVPNRun = filepath.Join(root, "run")
	diagnosticProcDir = filepath.Join(root, "proc")
	diagnosticReadFile = os.ReadFile
	diagnosticReadDir = os.ReadDir
	diagnosticOpenVPN = func(string) (string, error) { return "/usr/sbin/openvpn", nil }
	t.Cleanup(func() {
		diagnosticOpenVPN, diagnosticOpenVPNRun, diagnosticProcDir, diagnosticReadFile, diagnosticReadDir = oldLook, oldRun, oldProc, oldRead, oldDir
	})
	if err := os.MkdirAll(diagnosticOpenVPNRun, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diagnosticOpenVPNRun, "ov.pid"), []byte("99\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if item := openVPNStatus(); item.State != "stopped" {
		t.Fatalf("stale pid=%#v", item)
	}
	if err := os.MkdirAll(filepath.Join(diagnosticProcDir, "99"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(diagnosticProcDir, "99", "cmdline"), []byte("not-openvpn\x00"), 0644); err != nil {
		t.Fatal(err)
	}
	if item := openVPNStatus(); item.State != "stopped" {
		t.Fatalf("wrong process=%#v", item)
	}
	if err := os.WriteFile(filepath.Join(diagnosticProcDir, "99", "cmdline"), []byte("/usr/sbin/openvpn\x00"), 0644); err != nil {
		t.Fatal(err)
	}
	if item := openVPNStatus(); item.State != "running" {
		t.Fatalf("openvpn2=%#v", item)
	}
}

func TestMonitorProfileAutomaticAndInvalid(t *testing.T) {
	old := diagnosticReadFile
	t.Cleanup(func() { diagnosticReadFile = old })
	diagnosticReadFile = func(string) ([]byte, error) { return []byte("0\n"), nil }
	if got := monitorProfile(); got != "automatic" {
		t.Fatalf("automatic=%q", got)
	}
	diagnosticReadFile = func(string) ([]byte, error) { return []byte("-1\n"), nil }
	if got := monitorProfile(); got != "" {
		t.Fatalf("negative profile=%q", got)
	}
}
