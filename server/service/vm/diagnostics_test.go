package vm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNftHookChainsAreAnonymized(t *testing.T) {
	chains, err := parseNFTHookChains([]byte(`{"nftables":[{"table":{"family":"inet","name":"filter"}},{"chain":{"family":"inet","table":"filter","name":"input","type":"filter","hook":"input","policy":"drop"}},{"chain":{"family":"inet","table":"filter","name":"ordinary"}}]}`))
	if err != nil || len(chains) != 1 || chains[0].Hook != "input" || chains[0].Policy != "drop" {
		t.Fatalf("chains = %#v, %v", chains, err)
	}
	if chains[0].Label != "chain-1" || chains[0].Feature != "" {
		t.Fatalf("chain label = %#v", chains[0])
	}
}

func TestNftOwnTablesNameTheirFeature(t *testing.T) {
	chains, err := parseNFTHookChains([]byte(`{"nftables":[{"chain":{"family":"inet","table":"nkos_mss","name":"postrouting","hook":"postrouting","policy":"accept"}},{"chain":{"family":"inet","table":"nkos_secret_name","name":"x","hook":"input","policy":"accept"}}]}`))
	if err != nil || len(chains) != 2 || chains[0].Feature != "mss-clamp" || chains[1].Feature != "" {
		t.Fatalf("chains = %#v, %v", chains, err)
	}
	report := reportHookChains(append(chains, DiagnosticHookChain{Hook: "input", Feature: "leaked-name"}))
	if report[0].Feature != "mss-clamp" || report[2].Feature != "" {
		t.Fatalf("report = %#v", report)
	}
}

func TestDiagnosticsReportExcludesAdversarialValues(t *testing.T) {
	var snapshot DiagnosticsSnapshot
	snapshot.Versions.Application = "host.example Authorization: Bearer SECRET"
	snapshot.Versions.Image = "https://user:pass@host/path?api_key=QUERY"
	snapshot.Versions.Alpine = "password is multi word secret"
	snapshot.Versions.Kernel = "192.0.2.10 aa:bb:cc:dd:ee:ff"
	snapshot.Versions.Packages = []DiagnosticPackage{{Name: "host.example", Version: "SECRET", State: "installed"}}
	snapshot.Services = []DiagnosticService{{Name: "hostname", Desired: "SECRET", DiagnosticItem: DiagnosticItem{State: "running"}}}
	snapshot.USB.Selected = []string{"password", "keyboard"}
	snapshot.Firewall.HookChains = []DiagnosticHookChain{{Label: "hostname", Hook: "input", Policy: "drop"}}
	b, err := json.Marshal(diagnosticsReport(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, forbidden := range []string{"SECRET", "user:pass", "QUERY", "multi word", "192.0.2.10", "aa:bb:cc:dd:ee:ff", "host.example"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("report leaked %q: %s", forbidden, text)
		}
	}
}
