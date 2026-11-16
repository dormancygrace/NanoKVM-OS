package usbinternet

import (
	"encoding/json"
	"net/netip"
	"os"
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
	cfg := dnsConfig("/run/test", "10.20.30", false, 0)
	if !strings.Contains(cfg, "port=0\ndhcp-option=3\ndhcp-option=6\n") {
		t.Fatal(cfg)
	}
	active := dnsConfig("/run/test", "10.20.30", true, 0)
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
	rules := ruleset(netip.MustParsePrefix("10.20.30.0/24"), "wlan0", true, true, hostInput{})
	if strings.Contains(rules, "flags offload") || strings.Contains(rules, "flush ruleset") || !strings.Contains(rules, "flow add @usb_fast") {
		t.Fatal(rules)
	}
	local := ruleset(netip.MustParsePrefix("10.20.30.0/24"), "", false, true, hostInput{})
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

// The managed computer opens only what the USB features need on usb0.
func TestHostInputFilter(t *testing.T) {
	served := hostInput{DHCP: true, Web: 443}
	for _, tc := range []struct {
		name, uplink string
		in           hostInput
		want, not    []string
	}{
		{"local", "", served,
			[]string{"udp dport 67 accept", "tcp dport 443 accept", "meta nfproto ipv6 counter drop", "ct state { established, related } accept", "iifname \"usb0\" counter drop\n }\n chain forward"},
			[]string{"th dport 53", "dport 22", "dport 80 "}},
		{"sharing", "eth0", served, []string{"th dport 53 accept", "tcp dport 443 accept"}, nil},
		{"netboot", "eth0", hostInput{DHCP: true, Web: 8443, Netboot: 8070}, []string{"tcp dport 8070 accept", "tcp dport 8443 accept"}, []string{"dport 443 "}},
		{"no DHCP served", "", hostInput{Web: 443}, []string{"iifname \"usb0\" counter drop"}, []string{"dport 67"}},
		{"host access", "", hostInput{Open: true, DHCP: true, Web: 443}, nil, []string{"hook input"}},
	} {
		rules := ruleset(netip.MustParsePrefix("10.20.30.0/24"), tc.uplink, false, false, tc.in)
		for _, w := range tc.want {
			if !strings.Contains(rules, w) {
				t.Errorf("%s: missing %q in\n%s", tc.name, w, rules)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(rules, n) {
				t.Errorf("%s: unexpected %q in\n%s", tc.name, n, rules)
			}
		}
	}
	// Without the helper's address only the input filter: routing set up by
	// hand over usb0 stays as it is.
	if only := inputOnly(false, hostInput{Web: 443}); !strings.Contains(only, "hook input") || strings.Contains(only, "forward") || strings.Contains(only, "dport 67") {
		t.Errorf("input only:\n%s", only)
	}
	if only := inputOnly(true, hostInput{Open: true}); only != "" {
		t.Errorf("host access keeps a table:\n%s", only)
	}
	for _, tc := range []struct {
		yaml string
		port int
	}{
		{"", 443},
		{"proto: https\nport:\n  http: 80\n  https: 8443\n", 8443},
		{"proto: http\nport:\n  http: 8080\n  https: 443\n", 8080},
		{"port:\n  http: 8080\n  https: 443\n", 8080},
		{"proto: https\nport:\n  http: 0\n  https: 8443\n", 443},
		{"proto: http\nport:\n  http: 70000\n  https: 443\n", 443},
		{"{broken", 443},
	} {
		if got := webPort([]byte(tc.yaml)); got != tc.port {
			t.Errorf("webPort(%q) = %d, want %d", tc.yaml, got, tc.port)
		}
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
