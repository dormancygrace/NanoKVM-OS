package network

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

type ipv6Status struct {
	Enabled   bool          `json:"enabled"`
	Active    bool          `json:"active"`
	Supported bool          `json:"supported"`
	Addresses []ipv6Address `json:"addresses"`
}

func readIPv6Preference() (bool, error) {
	data, err := os.ReadFile(ipv6ConfigFile)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(string(data)) {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("invalid IPv6 configuration")
	}
}

func applyIPv6(enabled bool) error {
	value := "0"
	if enabled {
		value = "1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-s", "--", "apply", value)
	cmd.Stdin = strings.NewReader(ipv6BootScript)
	cmd.Env = append(os.Environ(), "NANOKVM_IPV6_SYSCTL="+ipv6SysctlRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("apply IPv6: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func writeIPv6Preference(enabled bool) error {
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
	value := "0\n"
	if enabled {
		value = "1\n"
	}
	if _, err = f.WriteString(value); err != nil {
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
	enabled, err := readIPv6Preference()
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
	return applyIPv6(enabled)
}

func getIPv6Status() (ipv6Status, error) {
	status := ipv6Status{Addresses: []ipv6Address{}}
	enabled, err := readIPv6Preference()
	if err != nil {
		return status, err
	}
	status.Enabled = enabled
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
	}
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	ipv6Mutation.Lock()
	defer ipv6Mutation.Unlock()
	previous, err := readIPv6Preference()
	if err != nil {
		rsp.ErrRsp(c, -1, "failed to read IPv6 configuration")
		return
	}
	if err = applyIPv6(*req.Enabled); err == nil {
		err = writeIPv6Preference(*req.Enabled)
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
