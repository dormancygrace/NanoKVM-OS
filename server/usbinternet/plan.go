// Package usbinternet owns only the usb0 sharing policy, never the gadget or
// the device's uplink configuration. All network commands run in the caller's
// namespace, which also permits isolated qualification without a NanoKVM.
package usbinternet

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"strings"

	"NanoKVM-Server/pkg/yamlconf"
)

const Table = "nkos_usb_internet"
const owner = "NanoKVM USB internet"

type Status struct {
	Enabled       bool   `json:"enabled"`
	State         string `json:"state"`
	Uplink        string `json:"uplink,omitempty"`
	Address       string `json:"address,omitempty"`
	FlowOffload   bool   `json:"flowOffload"`
	OffloadReason string `json:"offloadReason,omitempty"`
	IPv6          bool   `json:"ipv6"`
	// HostAccess: the computer may open every service over usb0 (HostAccess).
	HostAccess bool  `json:"hostAccess"`
	UpdatedAt  int64 `json:"updatedAt"`
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
				// The Wi-Fi access point's table (S30wifi) matches only wlan0
				// traffic, which USB flows never take.
				if !(c.Family == "inet" && c.Table == "nkos_mss" && trustedMSS(data)) &&
					!(c.Family == "inet" && c.Table == "nkos_wifi_ap") {
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

// hostInput is what the managed computer may open on the NanoKVM over usb0.
// The computer is not trusted: a compromised one must not reach the web
// interface, SSH or any other service of the KVM that manages it. Unless
// Open (usb.host_access), only replies and these new connections pass:
//   - DHCP, while the helper's dnsmasq hands out the computer's address;
//   - DNS, while sharing advertises the NanoKVM as the computer's resolver;
//   - the web port, for the sensors agent's reports and the serial console
//     helper's download: the server answers nothing else on usb0
//     (middleware.USBHostGate);
//   - the network boot menu's port, while network boot is on.
//
// IPv6 is dropped, as in the forward chain: the USB network is IPv4 only.
// The host watchdog's ARP requests and their answers never reach inet hooks.
type hostInput struct {
	Open    bool
	DHCP    bool
	Web     int
	Netboot int
}

func (in hostInput) chain(dns bool) string {
	if in.Open {
		return ""
	}
	var s strings.Builder
	s.WriteString(" chain input { type filter hook input priority filter; policy accept;\n")
	s.WriteString("  iifname \"usb0\" meta nfproto ipv6 counter drop\n")
	s.WriteString("  iifname \"usb0\" ct state { established, related } accept\n")
	if in.DHCP {
		s.WriteString("  iifname \"usb0\" udp dport 67 accept\n")
	}
	if dns {
		s.WriteString("  iifname \"usb0\" meta l4proto { tcp, udp } th dport 53 accept\n")
	}
	if in.Web > 0 {
		fmt.Fprintf(&s, "  iifname \"usb0\" tcp dport %d accept\n", in.Web)
	}
	if in.Netboot > 0 {
		fmt.Fprintf(&s, "  iifname \"usb0\" tcp dport %d accept\n", in.Netboot)
	}
	s.WriteString("  iifname \"usb0\" counter drop\n }\n")
	return s.String()
}

// webPort is the port the server's web interface listens on, read from
// server.yaml with the server's rules: the HTTPS port when proto is https,
// the HTTP port otherwise. A missing or unusable file is replaced by the
// server's defaults (HTTPS on 443), as config.validate does.
func webPort(data []byte) int {
	var proto string
	ports, err := yamlconf.ReadPorts(data)
	if values, perr := yamlconf.Parse(data); perr == nil {
		_ = yamlconf.Decode(values["proto"], &proto)
	}
	if err != nil || ports.HTTP < 1 || ports.HTTPS < 1 || ports.HTTP > 65535 || ports.HTTPS > 65535 {
		ports, proto = yamlconf.Ports{HTTP: 80, HTTPS: 443}, "https"
	}
	if proto == "https" {
		return ports.HTTPS
	}
	return ports.HTTP
}

// inputOnly is the table of a bound usb0 the helper does not set up
// (rndis.nodhcpd, no address yet): the input filter alone, so that routing
// configured by hand over usb0 is left alone. Empty with host access: the
// table goes.
func inputOnly(exists bool, in hostInput) string {
	chain := in.chain(false)
	if chain == "" {
		return ""
	}
	var s strings.Builder
	if exists {
		fmt.Fprintf(&s, "delete table inet %s\n", Table)
	}
	fmt.Fprintf(&s, "table inet %s {\n comment %q\n%s}\n", Table, owner, chain)
	return s.String()
}

func ruleset(subnet netip.Prefix, uplink string, flow, exists bool, in hostInput) string {
	var s strings.Builder
	if exists {
		fmt.Fprintf(&s, "delete table inet %s\n", Table)
	}
	fmt.Fprintf(&s, "table inet %s {\n comment %q\n", Table, owner)
	// dnsmasq answers DNS only while sharing (dnsConfig).
	s.WriteString(in.chain(uplink != ""))
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

// bootPort > 0 hands iPXE clients (DHCP option 175) the network boot menu on
// this address. Other clients, including the operating system booted from
// it, never see a boot file.
func dnsConfig(resolv, prefix string, sharing bool, bootPort int) string {
	cfg := fmt.Sprintf("interface=usb0\nexcept-interface=lo\nbind-interfaces\nlisten-address=%s.1\nno-hosts\nlog-facility=-\ndomain-needed\nbogus-priv\nuser=nobody\npid-file=\nleasefile-ro\nquiet-dhcp\ndhcp-authoritative\ndhcp-range=%s.100,%s.200,255.255.255.0,2m\n", prefix, prefix, prefix)
	if sharing {
		cfg += fmt.Sprintf("resolv-file=%s\ndhcp-option=3,%s.1\ndhcp-option=6,%s.1\n", resolv, prefix, prefix)
	} else {
		cfg += "port=0\ndhcp-option=3\ndhcp-option=6\n"
	}
	if bootPort > 0 {
		cfg += fmt.Sprintf("dhcp-match=set:ipxe,175\ndhcp-boot=tag:ipxe,http://%s.1:%d/boot.ipxe\n", prefix, bootPort)
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
