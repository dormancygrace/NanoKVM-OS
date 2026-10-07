package usbinternet

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubnetSelection(t *testing.T) {
	preferred := "10.22.33"
	occupied := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.31.0.0/24")}
	p, err := chooseSubnet(preferred, false, occupied)
	if err != nil || p.String() != "172.31.1.0/24" {
		t.Fatalf("alternate: %v %v", p, err)
	}
	if _, err = chooseSubnet(preferred, true, occupied); err == nil {
		t.Fatal("custom overlap accepted")
	}
	if _, err = chooseSubnet(preferred, false, []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}); err == nil {
		t.Fatal("no subnet should be available")
	}
	for _, invalid := range []string{"bad", "10.20.999", "0.0.0", "127.0.0", "224.1.1", "10.02.3"} {
		if _, err = chooseSubnet(invalid, false, nil); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
}
func TestOwnedTableAndForeignPolicies(t *testing.T) {
	for _, tc := range []struct {
		input                string
		owned, foreign, fail bool
	}{
		{`{"nftables":[]}`, false, false, false},
		{`{"nftables":[{"table":{"family":"inet","name":"nkos_usb_internet","comment":"NanoKVM USB internet"}}]}`, true, false, false},
		{`{"nftables":[{"table":{"family":"inet","name":"nkos_usb_internet"}}]}`, false, false, true},
		{`{"nftables":[{"chain":{"family":"inet","table":"vpn","hook":"forward"}}]}`, false, true, false},
		{`{"nftables":[{"chain":{"family":"ip","table":"vpn","hook":"postrouting"}}]}`, false, true, false},
		{`{"nftables":[{"chain":{"family":"inet","table":"vpn","hook":"input"}}]}`, false, false, false},
		{`broken`, false, false, true},
	} {
		owned, foreign, err := inspectRules([]byte(tc.input))
		if owned != tc.owned || foreign != tc.foreign || (err != nil) != tc.fail {
			t.Fatalf("%s: %v %v %v", tc.input, owned, foreign, err)
		}
	}
}
func TestNoGatewayOrIPv6WhenLocal(t *testing.T) {
	cfg := dnsConfig("/run/test", "10.20.30", false)
	if !strings.Contains(cfg, "port=0\ndhcp-option=3\ndhcp-option=6\n") {
		t.Fatal(cfg)
	}
	active := dnsConfig("/run/test", "10.20.30", true)
	if !strings.Contains(active, "dhcp-option=3,10.20.30.1") || !strings.Contains(active, "dhcp-option=6,10.20.30.1") {
		t.Fatal(active)
	}
	for _, s := range []string{cfg, active} {
		if strings.Contains(s, "enable-ra") || strings.Contains(s, "dhcp-range=::") {
			t.Fatal("IPv6 advertised")
		}
	}
}
func TestPolicyOffloadAndUSBOnlyRules(t *testing.T) {
	rules := ruleset(netip.MustParsePrefix("10.20.30.0/24"), "wlan0", true, true)
	if strings.Contains(rules, "flags offload") || strings.Contains(rules, "flush ruleset") || !strings.Contains(rules, "flow add @usb_fast") {
		t.Fatal(rules)
	}
	local := ruleset(netip.MustParsePrefix("10.20.30.0/24"), "", false, true)
	if strings.Contains(local, "masquerade") || strings.Contains(local, "flowtable") || !strings.Contains(local, "meta nfproto ipv6") {
		t.Fatal(local)
	}
	if !defaultPolicy([]byte(`[{"priority":0,"table":"local"},{"priority":32766,"table":"main"},{"priority":32767,"table":"default"}]`)) {
		t.Fatal("normal policy rejected")
	}
	if defaultPolicy([]byte(`[{"priority":0,"table":"local"},{"priority":100,"table":52}]`)) {
		t.Fatal("VPN policy allowed offload")
	}
}

func TestOnlyExactMSSPolicyIsCompatible(t *testing.T) {
	data, err := os.ReadFile("testdata/mss.json")
	if err != nil {
		t.Fatal(err)
	}
	_, foreign, err := inspectRules(data)
	if err != nil || foreign || !trustedMSS(data) {
		t.Fatal("package MSS policy rejected", foreign, err)
	}
	changed := strings.Replace(string(data), `"value":{"rt":{"key":"mtu"}}`, `"value":1200`, 1)
	_, foreign, err = inspectRules([]byte(changed))
	if err != nil || !foreign {
		t.Fatal("custom MSS policy permitted offload", foreign, err)
	}

	var withExtra struct {
		NFT []any `json:"nftables"`
	}
	if err = json.Unmarshal(data, &withExtra); err != nil {
		t.Fatal(err)
	}
	withExtra.NFT = append(withExtra.NFT, map[string]any{"rule": map[string]any{"family": "inet", "table": "nkos_mss", "chain": "postrouting", "expr": []any{map[string]any{"drop": nil}}}})
	duplicate, err := json.Marshal(withExtra)
	if err != nil {
		t.Fatal(err)
	}
	_, foreign, err = inspectRules(duplicate)
	if err != nil || !foreign {
		t.Fatal("additional foreign rule permitted offload", err)
	}

	if defaultPolicy([]byte(`[{"priority":0,"table":"local"},{"priority":32766,"table":"main","fwmark":1},{"priority":32767,"table":"default"}]`)) {
		t.Fatal("custom selectors permitted offload")
	}
}

func TestDaemonOwnershipAndStatus(t *testing.T) {
	dir := t.TempDir()
	r := Runtime{Boot: dir, Run: dir, Proc: filepath.Join(dir, "proc")}
	if r.Enabled() {
		t.Fatal("sharing defaults on")
	}
	if err := os.WriteFile(filepath.Join(dir, "usb.internet"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if s := r.ReadStatus(); !s.Enabled || s.State != "unavailable" || s.IPv6 {
		t.Fatal(s)
	}
	if err := r.status(Status{Enabled: true, State: "waiting-uplink"}); err != nil {
		t.Fatal(err)
	}
	if s := r.ReadStatus(); s.State != "waiting-uplink" {
		t.Fatal(s)
	}
	pidDir := filepath.Join(r.Proc, "42")
	if err := os.MkdirAll(pidDir, 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(dir, "dnsmasq.pid"): "42\n", filepath.Join(pidDir, "comm"): "dnsmasq\n", filepath.Join(pidDir, "cmdline"): "dnsmasq\x00--conf-file=/etc/other-dnsmasq.conf\x00"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, owned := r.ownedDaemon(); owned {
		t.Fatal("another daemon accepted")
	}
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte("dnsmasq\x00--conf-file="+filepath.Join(dir, "dnsmasq.conf")+"\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, owned := r.ownedDaemon(); !owned {
		t.Fatal("own daemon rejected")
	}
}
