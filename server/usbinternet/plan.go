// Package usbinternet owns only the usb0 sharing policy, never the gadget or
// the device's uplink configuration. All network commands run in the caller's
// namespace, which also permits isolated qualification without a NanoKVM.
package usbinternet

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
)

const Table = "nkos_usb_internet"
const owner = "NanoKVM USB internet"

var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

type Status struct {
	Enabled       bool   `json:"enabled"`
	State         string `json:"state"`
	Uplink        string `json:"uplink,omitempty"`
	Address       string `json:"address,omitempty"`
	FlowOffload   bool   `json:"flowOffload"`
	OffloadReason string `json:"offloadReason,omitempty"`
	IPv6          bool   `json:"ipv6"`
	UpdatedAt     int64  `json:"updatedAt"`
}

type route struct {
	Dst     string `json:"dst"`
	Dev     string `json:"dev"`
	Gateway string `json:"gateway"`
	Type    string `json:"type"`
}

func overlaps(a, b netip.Prefix) bool { return a.Contains(b.Addr()) || b.Contains(a.Addr()) }

// Route prefixes include VPN and non-main tables. Default routes are uplinks,
// not occupied LANs. Explicit custom prefixes fail closed on overlap.
func chooseSubnet(preferred string, custom bool, occupied []netip.Prefix) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(preferred + ".0/24")
	if err != nil || !p.Addr().Is4() || p.Addr().IsLoopback() || p.Addr().IsMulticast() || p.Addr().IsUnspecified() {
		return netip.Prefix{}, fmt.Errorf("invalid USB prefix")
	}
	free := func(p netip.Prefix) bool {
		for _, o := range occupied {
			if o.Bits() > 0 && overlaps(p, o) {
				return false
			}
		}
		return true
	}
	if free(p) {
		return p, nil
	}
	if custom {
		return netip.Prefix{}, fmt.Errorf("custom USB subnet overlaps another network")
	}
	// Bounded, deterministic alternatives; preserve the UID-derived prefix first.
	for _, base := range []string{"172.31", "192.168", "10.254", "172.30"} {
		for n := 0; n < 256; n++ {
			candidate := netip.MustParsePrefix(fmt.Sprintf("%s.%d.0/24", base, n))
			if free(candidate) {
				return candidate, nil
			}
		}
	}
	return netip.Prefix{}, fmt.Errorf("no unused USB subnet")
}

func subnetPrefix(p netip.Prefix) string {
	s := p.Addr().String()
	return s[:strings.LastIndex(s, ".")]
}

// Offloading bypasses subsequent netfilter hooks. Do not offload through any
// foreign transit/ingress chain or non-default policy routing (VPNs included).
func inspectRules(data []byte) (exists, foreign bool, err error) {
	var rules struct {
		NFT []struct {
			Table *struct{ Family, Name, Comment string } `json:"table"`
			Chain *struct{ Family, Table, Hook string }   `json:"chain"`
		} `json:"nftables"`
	}
	if err = json.Unmarshal(data, &rules); err != nil {
		return
	}
	for _, r := range rules.NFT {
		if t := r.Table; t != nil && t.Family == "inet" && t.Name == Table {
			if t.Comment != owner {
				err = fmt.Errorf("nft table name is already owned by another service")
				return
			}
			exists = true
		}
		if c := r.Chain; c != nil && !(c.Family == "inet" && c.Table == Table) {
			switch c.Hook {
			case "ingress", "prerouting", "forward", "postrouting":
				if !(c.Family == "inet" && c.Table == "nkos_mss" && trustedMSS(data)) {
					foreign = true
				}
			}
		}
	}
	return
}

func defaultPolicy(data []byte) bool {

	var rules []map[string]json.RawMessage
	if json.Unmarshal(data, &rules) != nil || len(rules) != 3 {
		return false
	}
	seen := map[int]bool{}
	for _, r := range rules {
		var priority int
		var table any
		if json.Unmarshal(r["priority"], &priority) != nil || json.Unmarshal(r["table"], &table) != nil {
			return false
		}
		for key, value := range r {
			switch key {
			case "priority", "table":
			case "src", "dst":
				if string(value) != `"all"` {
					return false
				}
			default:
				return false
			}
		}
		name := fmt.Sprint(table)
		if seen[priority] || !((priority == 0 && (name == "local" || name == "255")) || (priority == 32766 && (name == "main" || name == "254")) || (priority == 32767 && (name == "default" || name == "253"))) {
			return false
		}
		seen[priority] = true
	}
	return true
}

func ruleset(subnet netip.Prefix, uplink string, flow, exists bool) string {
	var s strings.Builder
	if exists {
		fmt.Fprintf(&s, "delete table inet %s\n", Table)
	}
	fmt.Fprintf(&s, "table inet %s {\n comment %q\n", Table, owner)
	if flow {
		fmt.Fprintf(&s, " flowtable usb_fast { hook ingress priority 0; devices = { usb0, %q }; }\n", uplink)
	}
	// A local-only guard also prevents accidental USB routing when a VPN has
	// enabled forwarding globally. It never filters another interface's traffic.
	s.WriteString(" chain forward { type filter hook forward priority 30000; policy accept;\n")
	s.WriteString("  iifname \"usb0\" meta nfproto ipv6 counter drop\n  oifname \"usb0\" meta nfproto ipv6 counter drop\n")
	if uplink != "" {
		s.WriteString("  iifname \"usb0\" ct state invalid counter drop\n")
		if flow {
			fmt.Fprintf(&s, "  iifname %q oifname \"usb0\" ip daddr %s ct state established ct mark 0 meta l4proto { tcp, udp } flow add @usb_fast\n", uplink, subnet)
		}
		fmt.Fprintf(&s, "  iifname \"usb0\" oifname %q ip saddr %s tcp flags syn tcp option maxseg size set rt mtu\n", uplink, subnet)
		fmt.Fprintf(&s, "  iifname \"usb0\" oifname %q ip saddr %s ct state { new, established, related } counter accept\n", uplink, subnet)
		fmt.Fprintf(&s, "  iifname %q oifname \"usb0\" ip daddr %s ct state { established, related } counter accept\n", uplink, subnet)
	}
	s.WriteString("  iifname \"usb0\" counter drop\n  oifname \"usb0\" counter drop\n }\n")
	if uplink != "" {
		fmt.Fprintf(&s, " chain nat { type nat hook postrouting priority srcnat; policy accept; iifname \"usb0\" oifname %q ip saddr %s counter masquerade; }\n", uplink, subnet)
	}
	s.WriteString("}\n")
	return s.String()
}

func dnsConfig(resolv, prefix string, sharing bool) string {
	cfg := fmt.Sprintf("interface=usb0\nexcept-interface=lo\nbind-interfaces\nlisten-address=%s.1\nno-hosts\nlog-facility=-\ndomain-needed\nbogus-priv\nuser=nobody\npid-file=\nleasefile-ro\nquiet-dhcp\ndhcp-authoritative\ndhcp-range=%s.100,%s.200,255.255.255.0,2m\n", prefix, prefix, prefix)
	if sharing {
		cfg += fmt.Sprintf("resolv-file=%s\ndhcp-option=3,%s.1\ndhcp-option=6,%s.1\n", resolv, prefix, prefix)
	} else {
		cfg += "port=0\ndhcp-option=3\ndhcp-option=6\n"
	}
	// No enable-ra, DHCPv6 range or IPv6 DNS/router advertisement.
	return cfg
}

// Only the exact package SYN-only MSS ceiling is safe to bypass for established
// packets. A same-named table with additional/custom rules is still foreign.
func trustedMSS(data []byte) bool {
	var rules struct {
		NFT []map[string]json.RawMessage `json:"nftables"`
	}
	if json.Unmarshal(data, &rules) != nil {
		return false
	}
	chains, entries := 0, 0
	for _, object := range rules.NFT {
		for kind, raw := range object {
			var item struct {
				Family, Table, Name, Type, Hook, Policy, Chain string
				Prio                                           int
				Expr                                           []any
			}
			if json.Unmarshal(raw, &item) != nil {
				return false
			}
			if item.Family != "inet" || (item.Table != "nkos_mss" && !(kind == "table" && item.Name == "nkos_mss")) {
				continue
			}
			switch kind {
			case "table":
			case "chain":
				chains++
				if item.Name != "postrouting" || item.Type != "filter" || item.Hook != "postrouting" || item.Prio != -150 || item.Policy != "accept" {
					return false
				}
			case "rule":
				entries++
				if item.Chain != "postrouting" {
					return false
				}
				var expression []any
				counters := 0
				for _, e := range item.Expr {
					if m, ok := e.(map[string]any); ok && len(m) == 1 && m["counter"] != nil {
						counters++
						continue
					}
					expression = append(expression, e)
				}
				var expected []any
				_ = json.Unmarshal([]byte(`[{"match":{"op":"!=","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"match":{"op":"==","left":{"&":[{"payload":{"protocol":"tcp","field":"flags"}},{"|":["syn","rst"]}]},"right":"syn"}},{"mangle":{"key":{"tcp option":{"name":"maxseg","field":"size"}},"value":{"rt":{"key":"mtu"}}}}]`), &expected)
				if counters != 1 || !reflect.DeepEqual(expression, expected) {
					return false
				}
			default:
				return false
			}
		}
	}
	return chains == 1 && entries == 1
}
