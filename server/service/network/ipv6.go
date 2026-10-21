package network

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

//go:embed scripts/S02ipv6
var ipv6BootScript string

var ipv6ConfigFile = "/etc/kvm/network/ipv6"
var ipv6SysctlRoot = "/proc/sys/net/ipv6/conf"
var ipv6Mutation sync.Mutex

type ipv6Address struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Scope     string `json:"scope"`
	MTU       int    `json:"mtu"`
	// Where a global address of Ethernet or Wi-Fi came from: slaac,
	// dhcpv6 or static; empty for link-local and other interfaces.
	Source string `json:"source,omitempty"`
}

// The DHCPv6 client (scripts/nanokvm-dhcpv6-event) and S02ipv6 record the
// address they set here.
var ipv6RunDir = "/run"

// recordedIPv6 is the address in a record file's first word, if any.
func recordedIPv6(file string) netip.Addr {
	data, err := os.ReadFile(filepath.Join(ipv6RunDir, file))
	if err != nil {
		return netip.Addr{}
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return netip.Addr{}
	}
	prefix, err := netip.ParsePrefix(fields[0])
	if err != nil {
		return netip.Addr{}
	}
	return prefix.Addr()
}

func ipv6Source(iface string, ip netip.Addr) string {
	if !slices.Contains(ipv6Interfaces, iface) || ip.IsLinkLocalUnicast() {
		return ""
	}
	switch ip {
	case recordedIPv6("udhcpc6." + iface + ".addr"):
		return ipv6DHCPv6
	case recordedIPv6("ipv6-static." + iface):
		return ipv6Static
	}
	return "slaac"
}

// Ethernet and Wi-Fi have their own IPv6 setting; other interfaces (USB
// network, VPNs) have IPv6 when either of them has (scripts/S02ipv6).
var ipv6Interfaces = []string{"eth0", "wlan0"}

// How an interface with IPv6 gets its address, as for IPv4: automatically
// (SLAAC from router advertisements and DHCPv6) or a static address.
const (
	ipv6DHCPv6 = "dhcpv6"
	ipv6Static = "static"
)

type ipv6Interface struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Mode is dhcpv6 or static while IPv6 is on, empty while it is off.
	Mode string `json:"mode"`
	// The static address (with its prefix length) and gateway; kept while
	// another mode is chosen, so that they come back with static.
	Address string `json:"address"`
	Gateway string `json:"gateway"`
	Active  bool   `json:"active"`
	Present bool   `json:"present"`
}

// ipv6Setting is the IPv6 of one managed interface.
type ipv6Setting struct {
	Mode    string // "", ipv6DHCPv6 or ipv6Static
	Address string // static address with prefix length, such as 2001:db8::10/64
	Gateway string // optional static gateway
}

// normalized checks a static address and gateway and returns the setting
// with both in canonical form, as netip prints them. They are passed to ip(8)
// and saved, so only the parsed value goes on, never the text as entered.
func (s ipv6Setting) normalized() (ipv6Setting, error) {
	if s.Address != "" {
		prefix, err := netip.ParsePrefix(s.Address)
		if err != nil || !prefix.Addr().Is6() || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" ||
			prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() || prefix.Addr().IsLoopback() {
			return s, fmt.Errorf("invalid IPv6 address")
		}
		// Keeps the host bits: 2001:db8::10/64 stays itself, not 2001:db8::/64.
		s.Address = prefix.String()
	}
	if s.Gateway != "" {
		gateway, err := netip.ParseAddr(s.Gateway)
		if err != nil || !gateway.Is6() || gateway.Is4In6() || gateway.Zone() != "" ||
			gateway.IsUnspecified() || gateway.IsMulticast() || gateway.IsLoopback() {
			return s, fmt.Errorf("invalid IPv6 gateway")
		}
		s.Gateway = gateway.String()
	}
	if s.Mode == ipv6Static && s.Address == "" {
		return s, fmt.Errorf("static IPv6 needs an address")
	}
	return s, nil
}

type ipv6Status struct {
	// Enabled says whether any interface has IPv6, for pages from before
	// the per-interface setting.
	Enabled    bool            `json:"enabled"`
	Active     bool            `json:"active"`
	Supported  bool            `json:"supported"`
	Addresses  []ipv6Address   `json:"addresses"`
	Interfaces []ipv6Interface `json:"interfaces"`
}

// ipv6Policy is the IPv6 setting of each managed interface; a missing one is off.
type ipv6Policy map[string]ipv6Setting

// The configuration file's value of each mode: 1 is automatic, as it was
// before static addresses.
var ipv6Values = map[string]string{"0": "", "1": ipv6DHCPv6, "static": ipv6Static}

// parseIPv6Policy reads "eth0=static eth0.address=2001:db8::10/64
// eth0.gateway=2001:db8::1 wlan0=1"; an older "1" or "0" sets both.
func parseIPv6Policy(text string) (ipv6Policy, error) {
	policy := ipv6Policy{}
	invalid := fmt.Errorf("invalid IPv6 configuration")
	for _, field := range strings.Fields(text) {
		if field == "0" || field == "1" {
			for _, name := range ipv6Interfaces {
				setting := policy[name]
				setting.Mode = ipv6Values[field]
				policy[name] = setting
			}
			continue
		}
		key, value, ok := strings.Cut(field, "=")
		name, attribute, _ := strings.Cut(key, ".")
		if !ok || !slices.Contains(ipv6Interfaces, name) {
			return nil, invalid
		}
		setting := policy[name]
		switch attribute {
		case "":
			mode, known := ipv6Values[value]
			if !known {
				return nil, invalid
			}
			setting.Mode = mode
		case "address":
			setting.Address = value
		case "gateway":
			setting.Gateway = value
		default:
			return nil, invalid
		}
		policy[name] = setting
	}
	for name, setting := range policy {
		normalized, err := setting.normalized()
		if err != nil {
			return nil, invalid
		}
		policy[name] = normalized
	}
	return policy, nil
}

func (p ipv6Policy) any() bool {
	for _, name := range ipv6Interfaces {
		if p[name].Mode != "" {
			return true
		}
	}
	return false
}

func (p ipv6Policy) words() []string {
	words := make([]string, 0, 3*len(ipv6Interfaces))
	for _, name := range ipv6Interfaces {
		setting := p[name]
		value := "0"
		switch setting.Mode {
		case ipv6DHCPv6:
			value = "1"
		case ipv6Static:
			value = "static"
		}
		words = append(words, name+"="+value)
		if setting.Address != "" {
			words = append(words, name+".address="+setting.Address)
		}
		if setting.Gateway != "" {
			words = append(words, name+".gateway="+setting.Gateway)
		}
	}
	return words
}

func readIPv6Policy() (ipv6Policy, error) {
	data, err := os.ReadFile(ipv6ConfigFile)
	if os.IsNotExist(err) {
		return ipv6Policy{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseIPv6Policy(string(data))
}

// applyIPv6 runs S02ipv6 on a policy. The policy reaches the script in a
// file, as the saved one does at boot, never on its command line.
func applyIPv6(policy ipv6Policy) error {
	file, err := os.CreateTemp("", "nanokvm-ipv6-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(strings.Join(policy.words(), "\n") + "\n")
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-s", "--", "start")
	cmd.Stdin = strings.NewReader(ipv6BootScript)
	cmd.Env = append(os.Environ(), "NANOKVM_IPV6_SYSCTL="+ipv6SysctlRoot, "NANOKVM_IPV6_CONF="+file.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply IPv6: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func writeIPv6Policy(policy ipv6Policy) error {
	dir := filepath.Dir(ipv6ConfigFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".ipv6-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.WriteString(strings.Join(policy.words(), "\n") + "\n"); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), ipv6ConfigFile)
}

// InitializeIPv6 also installs the early boot hook for application-only upgrades.
func InitializeIPv6() error {
	ipv6Mutation.Lock()
	defer ipv6Mutation.Unlock()
	policy, err := readIPv6Policy()
	if err != nil {
		return err
	}
	const path = "/etc/init.d/S02ipv6"
	data, err := os.ReadFile(path)
	if err != nil || string(data) != ipv6BootScript {
		if err := os.WriteFile(path+".new", []byte(ipv6BootScript), 0755); err != nil {
			return err
		}
		if err := os.Rename(path+".new", path); err != nil {
			return err
		}
	}
	if !policy.any() {
		if done, err := applyIPv6Off(); done || err != nil {
			return err
		}
	}
	return applyIPv6(policy)
}

// applyIPv6Off does what S02ipv6 start does for a policy with IPv6 off when
// there is nothing to undo, no DHCPv6 client and no static address: only
// sysctl values. The script costs about a second of the SG2002 on every
// server start, before the server listens. It reports false when the script
// must run.
func applyIPv6Off() (bool, error) {
	runDir := func(env string) string {
		if dir := os.Getenv(env); dir != "" {
			return dir
		}
		return "/run"
	}
	for _, name := range ipv6Interfaces {
		for _, path := range []string{
			filepath.Join(runDir("NANOKVM_DHCPV6_RUN"), "udhcpc6."+name+".pid"),
			filepath.Join(runDir("NANOKVM_IPV6_RUN"), "ipv6-static."+name),
		} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				return false, nil
			}
		}
	}
	entries, err := os.ReadDir(ipv6SysctlRoot)
	if os.IsNotExist(err) {
		// A kernel without IPv6 already satisfies a policy with IPv6 off.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	// As in the script: only an existing value, and only when it differs.
	set := func(name, key, value string) error {
		path := filepath.Join(ipv6SysctlRoot, name, key)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || strings.TrimSpace(string(data)) == value {
			return err
		}
		return os.WriteFile(path, []byte(value+"\n"), 0o644)
	}
	// "all" rewrites every interface, so it comes first.
	for _, name := range []string{"all", "default"} {
		if err := set(name, "disable_ipv6", "1"); err != nil {
			return true, err
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == "all" || name == "default" {
			continue
		}
		value := "1"
		if name == "lo" {
			value = "0" // keep ::1
		}
		if err := set(name, "disable_ipv6", value); err != nil {
			return true, err
		}
		if slices.Contains(ipv6Interfaces, name) {
			if err := set(name, "keep_addr_on_down", "0"); err != nil {
				return true, err
			}
			if err := set(name, "autoconf", "1"); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

func getIPv6Status() (ipv6Status, error) {
	status := ipv6Status{Addresses: []ipv6Address{}, Interfaces: []ipv6Interface{}}
	policy, err := readIPv6Policy()
	if err != nil {
		return status, err
	}
	status.Enabled = policy.any()
	for _, name := range ipv6Interfaces {
		state, err := os.ReadFile(filepath.Join(ipv6SysctlRoot, name, "disable_ipv6"))
		setting := policy[name]
		status.Interfaces = append(status.Interfaces, ipv6Interface{
			Name:    name,
			Enabled: setting.Mode != "",
			Mode:    setting.Mode,
			Address: setting.Address,
			Gateway: setting.Gateway,
			Active:  err == nil && strings.TrimSpace(string(state)) == "0",
			Present: err == nil,
		})
	}
	state, err := os.ReadFile(filepath.Join(ipv6SysctlRoot, "default/disable_ipv6"))
	if os.IsNotExist(err) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Supported = true
	status.Active = strings.TrimSpace(string(state)) == "0"
	interfaces, err := net.Interfaces()
	if err != nil {
		return status, err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return status, err
		}
		mtuData, _ := os.ReadFile(filepath.Join(ipv6SysctlRoot, iface.Name, "mtu"))
		mtu, _ := strconv.Atoi(strings.TrimSpace(string(mtuData)))
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip.To4() != nil || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}
			scope := "global"
			if ip.IsLinkLocalUnicast() {
				scope = "linkLocal"
			} else if ip.IsPrivate() {
				scope = "private"
			}
			parsed, _ := netip.AddrFromSlice(ip)
			status.Addresses = append(status.Addresses, ipv6Address{
				Interface: iface.Name,
				Address:   addr.String(),
				Scope:     scope,
				MTU:       mtu,
				Source:    ipv6Source(iface.Name, parsed.Unmap()),
			})
		}
	}
	return status, nil
}

func (s *Service) GetIPv6(c *gin.Context) {
	ipv6Mutation.Lock()
	defer ipv6Mutation.Unlock()
	var rsp proto.Response
	status, err := getIPv6Status()
	if err != nil {
		rsp.ErrRsp(c, -1, "failed to read IPv6 status")
		return
	}
	rsp.OkRspWithData(c, status)
}

func (s *Service) SetIPv6(c *gin.Context) {
	var req struct {
		Enabled *bool `json:"enabled" validate:"required"`
		// One managed interface; none sets them all, as pages from before
		// the per-interface setting do.
		Interface string `json:"interface"`
		// dhcpv6 or static when enabling; none keeps the interface's mode,
		// or DHCPv6 for an interface that was off. Address and gateway are
		// the static ones and are kept when omitted.
		Mode    string  `json:"mode"`
		Address *string `json:"address"`
		Gateway *string `json:"gateway"`
	}
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if (req.Interface != "" && !slices.Contains(ipv6Interfaces, req.Interface)) ||
		(req.Mode != "" && req.Mode != ipv6DHCPv6 && req.Mode != ipv6Static) ||
		(req.Interface == "" && (req.Mode != "" || req.Address != nil || req.Gateway != nil)) {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	ipv6Mutation.Lock()
	defer ipv6Mutation.Unlock()
	previous, err := readIPv6Policy()
	if err != nil {
		rsp.ErrRsp(c, -1, "failed to read IPv6 configuration")
		return
	}
	next := ipv6Policy{}
	for _, name := range ipv6Interfaces {
		setting := previous[name]
		if req.Interface == "" || req.Interface == name {
			if req.Address != nil {
				setting.Address = strings.TrimSpace(*req.Address)
			}
			if req.Gateway != nil {
				setting.Gateway = strings.TrimSpace(*req.Gateway)
			}
			switch {
			case !*req.Enabled:
				setting.Mode = ""
			case req.Mode != "":
				setting.Mode = req.Mode
			case setting.Mode == "":
				setting.Mode = ipv6DHCPv6
			}
			normalized, err := setting.normalized()
			if err != nil {
				rsp.ErrRsp(c, -1, err.Error())
				return
			}
			setting = normalized
		}
		next[name] = setting
	}
	if err = applyIPv6(next); err == nil {
		err = writeIPv6Policy(next)
	}
	if err != nil {
		rollbackErr := applyIPv6(previous)
		if rollbackErr != nil {
			rsp.ErrRsp(c, -2, "IPv6 update and recovery failed")
			return
		}
		rsp.ErrRsp(c, -2, "failed to apply IPv6 configuration")
		return
	}
	rsp.OkRsp(c)
}
