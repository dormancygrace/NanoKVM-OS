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

// validate checks a static address and gateway; they are passed to ip(8).
func (s ipv6Setting) validate() error {
	if s.Address != "" {
		prefix, err := netip.ParsePrefix(s.Address)
		if err != nil || !prefix.Addr().Is6() || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" ||
			prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() || prefix.Addr().IsLoopback() {
			return fmt.Errorf("invalid IPv6 address")
		}
	}
	if s.Gateway != "" {
		gateway, err := netip.ParseAddr(s.Gateway)
		if err != nil || !gateway.Is6() || gateway.Is4In6() || gateway.Zone() != "" ||
			gateway.IsUnspecified() || gateway.IsMulticast() || gateway.IsLoopback() {
			return fmt.Errorf("invalid IPv6 gateway")
		}
	}
	if s.Mode == ipv6Static && s.Address == "" {
		return fmt.Errorf("static IPv6 needs an address")
	}
	return nil
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
	for _, setting := range policy {
		if setting.validate() != nil {
			return nil, invalid
		}
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

func applyIPv6(policy ipv6Policy) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := append([]string{"-s", "--", "apply"}, policy.words()...)
	cmd := exec.CommandContext(ctx, "/bin/sh", args...)
	cmd.Stdin = strings.NewReader(ipv6BootScript)
	cmd.Env = append(os.Environ(), "NANOKVM_IPV6_SYSCTL="+ipv6SysctlRoot)
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
	return applyIPv6(policy)
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
			status.Addresses = append(status.Addresses, ipv6Address{iface.Name, addr.String(), scope, mtu})
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
			if err := setting.validate(); err != nil {
				rsp.ErrRsp(c, -1, err.Error())
				return
			}
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
