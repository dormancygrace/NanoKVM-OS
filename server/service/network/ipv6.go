package network

import (
	"context"
	_ "embed"
	"fmt"
	"net"
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

type ipv6Interface struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Active  bool   `json:"active"`
	Present bool   `json:"present"`
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
type ipv6Policy map[string]bool

// parseIPv6Policy reads "eth0=1 wlan0=0"; an older "1" or "0" sets both.
func parseIPv6Policy(text string) (ipv6Policy, error) {
	policy := ipv6Policy{}
	for _, field := range strings.Fields(text) {
		if field == "0" || field == "1" {
			for _, name := range ipv6Interfaces {
				policy[name] = field == "1"
			}
			continue
		}
		name, value, ok := strings.Cut(field, "=")
		if !ok || !slices.Contains(ipv6Interfaces, name) || (value != "0" && value != "1") {
			return nil, fmt.Errorf("invalid IPv6 configuration")
		}
		policy[name] = value == "1"
	}
	return policy, nil
}

func (p ipv6Policy) any() bool {
	for _, name := range ipv6Interfaces {
		if p[name] {
			return true
		}
	}
	return false
}

func (p ipv6Policy) words() []string {
	words := make([]string, 0, len(ipv6Interfaces))
	for _, name := range ipv6Interfaces {
		value := "0"
		if p[name] {
			value = "1"
		}
		words = append(words, name+"="+value)
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
		status.Interfaces = append(status.Interfaces, ipv6Interface{
			Name:    name,
			Enabled: policy[name],
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
	}
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if req.Interface != "" && !slices.Contains(ipv6Interfaces, req.Interface) {
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
		next[name] = previous[name]
		if req.Interface == "" || req.Interface == name {
			next[name] = *req.Enabled
		}
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
