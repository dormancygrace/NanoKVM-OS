package network

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	ethernetModeDHCP   = "dhcp"
	ethernetModeStatic = "static"

	ethernetInterface  = "eth0"
	ethernetInitScript = "/etc/init.d/S30eth"
)

var ethernetConfigFile = "/boot/eth.nodhcp"
var ethernetDisabledFile = "/boot/eth.disabled"
var ethernetCarrierFile = "/sys/class/net/eth0/carrier"
var ethernetFlagsFile = "/sys/class/net/eth0/flags"

func (s *Service) GetEthernet(c *gin.Context) {
	var rsp proto.Response

	config, err := readEthernetConfig()
	if err != nil {
		log.Errorf("failed to read ethernet config: %s", err)
		rsp.ErrRsp(c, -1, "failed to read ethernet configuration")
		return
	}

	rsp.OkRspWithData(c, config)
}

func (s *Service) SetEthernet(c *gin.Context) {
	var req proto.SetEthernetReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	config := proto.EthernetConfig{Enabled: *req.Enabled, Mode: req.Mode, Interface: ethernetInterface}
	if req.Mode == ethernetModeStatic {
		address, gateway, err := validateStaticEthernet(req.Address, req.SubnetMask, req.Gateway)
		if err != nil {
			rsp.ErrRsp(c, -1, err.Error())
			return
		}
		config.Address = address.Addr().String()
		config.SubnetMask = prefixToSubnetMask(address.Bits())
		config.Gateway = gateway.String()
	}
	if req.VLANEnabled != nil {
		config.VLANEnabled = *req.VLANEnabled
		config.VLANID = req.VLANID
		if config.VLANEnabled && (config.VLANID < 1 || config.VLANID > 4094) {
			rsp.ErrRsp(c, -1, "VLAN ID must be between 1 and 4094")
			return
		}
	}

	if err := writeEthernetConfig(config); err != nil {
		log.Errorf("failed to write ethernet config: %s", err)
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	if req.VLANEnabled != nil {
		if err := writeEthernetVLAN(config.VLANEnabled, config.VLANID); err != nil {
			log.Errorf("failed to write ethernet VLAN: %s", err)
			rsp.ErrRsp(c, -2, err.Error())
			return
		}
	}
	if err := writeEthernetEnabled(config.Enabled); err != nil {
		log.Errorf("failed to write ethernet enabled state: %s", err)
		rsp.ErrRsp(c, -2, err.Error())
		return
	}

	// Delay the restart so the API response is sent before its own network path is replaced.
	go restartEthernetAfterResponse()
	_ = exec.Command("sync").Run()

	rsp.OkRsp(c)
	log.Infof("set ethernet config: enabled=%t mode=%s address=%s gateway=%s", config.Enabled, config.Mode, config.Address, config.Gateway)
}

func readEthernetConfig() (proto.EthernetConfig, error) {
	enabled, err := readEthernetEnabled()
	if err != nil {
		return proto.EthernetConfig{}, err
	}
	config := proto.EthernetConfig{
		Enabled:   enabled,
		AdminUp:   readEthernetAdminUp(),
		LinkUp:    readEthernetCarrier(),
		Mode:      ethernetModeDHCP,
		Interface: ethernetInterface,
	}
	config.VLANEnabled, config.VLANID, err = readEthernetVLAN()
	if err != nil {
		return config, err
	}
	file, err := os.Open(ethernetConfigFile)
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimSuffix(scanner.Text(), "\r"))
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = strings.TrimSpace(line[:comment])
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) > 2 {
			return config, fmt.Errorf("invalid ethernet configuration")
		}

		prefix, err := netip.ParsePrefix(fields[0])
		if err != nil || !prefix.Addr().Is4() {
			return config, fmt.Errorf("invalid ethernet configuration")
		}
		if prefix.Bits() < 1 || prefix.Bits() > 30 {
			return config, fmt.Errorf("invalid ethernet configuration")
		}

		gateway := prefix.Masked().Addr().Next()
		if len(fields) == 2 {
			gateway, err = netip.ParseAddr(fields[1])
			if err != nil || !gateway.Is4() {
				return config, fmt.Errorf("invalid ethernet configuration")
			}
		}

		config.Mode = ethernetModeStatic
		config.Address = prefix.Addr().String()
		config.SubnetMask = prefixToSubnetMask(prefix.Bits())
		config.Gateway = gateway.String()
		return config, nil
	}

	if err := scanner.Err(); err != nil {
		return config, err
	}
	return config, nil
}

func readEthernetEnabled() (bool, error) {
	_, err := os.Stat(ethernetDisabledFile)
	if err == nil {
		return false, nil
	}
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, fmt.Errorf("failed to read ethernet enabled state: %w", err)
}

func readEthernetCarrier() bool {
	contents, err := os.ReadFile(ethernetCarrierFile)
	return err == nil && strings.TrimSpace(string(contents)) == "1"
}

func readEthernetAdminUp() bool {
	contents, err := os.ReadFile(ethernetFlagsFile)
	if err != nil {
		return false
	}
	flags, err := strconv.ParseUint(strings.TrimSpace(string(contents)), 0, 32)
	return err == nil && flags&1 != 0 // IFF_UP
}

func writeEthernetEnabled(enabled bool) error {
	if enabled {
		if err := os.Remove(ethernetDisabledFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to enable ethernet: %w", err)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(ethernetDisabledFile), 0o755); err != nil {
		return fmt.Errorf("failed to create ethernet configuration directory: %w", err)
	}
	tmpFile := ethernetDisabledFile + ".tmp"
	if err := os.WriteFile(tmpFile, nil, 0o600); err != nil {
		return fmt.Errorf("failed to write ethernet enabled state: %w", err)
	}
	if err := os.Rename(tmpFile, ethernetDisabledFile); err != nil {
		return fmt.Errorf("failed to save ethernet enabled state: %w", err)
	}
	return nil
}

func writeEthernetConfig(config proto.EthernetConfig) error {
	switch config.Mode {
	case ethernetModeDHCP:
		if err := os.Remove(ethernetConfigFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to enable dhcp: %w", err)
		}
		return nil
	case ethernetModeStatic:
		prefix, gateway, err := validateStaticEthernet(config.Address, config.SubnetMask, config.Gateway)
		if err != nil {
			return err
		}

		contents := fmt.Sprintf("%s %s\n", prefix.String(), gateway.String())
		tmpFile := ethernetConfigFile + ".tmp"
		if err := os.MkdirAll(filepath.Dir(ethernetConfigFile), 0o755); err != nil {
			return fmt.Errorf("failed to create ethernet configuration directory: %w", err)
		}
		if err := os.WriteFile(tmpFile, []byte(contents), 0o600); err != nil {
			return fmt.Errorf("failed to write ethernet configuration: %w", err)
		}
		if err := os.Rename(tmpFile, ethernetConfigFile); err != nil {
			return fmt.Errorf("failed to save ethernet configuration: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("invalid ethernet mode")
	}
}

func validateStaticEthernet(address string, subnetMask string, gateway string) (netip.Prefix, netip.Addr, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil || !ip.Is4() || !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("invalid static IP address")
	}
	prefixBits, err := subnetMaskToPrefix(subnetMask)
	if err != nil {
		return netip.Prefix{}, netip.Addr{}, err
	}
	prefix := netip.PrefixFrom(ip, prefixBits)
	if ip == prefix.Masked().Addr() || ip == ipv4Broadcast(prefix) {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("invalid static IP address")
	}

	gw, err := netip.ParseAddr(strings.TrimSpace(gateway))
	if err != nil || !gw.Is4() || !gw.IsValid() || gw.IsUnspecified() || gw.IsLoopback() || gw.IsMulticast() {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("invalid gateway")
	}
	if !prefix.Contains(gw) || gw == ip || gw == prefix.Masked().Addr() || gw == ipv4Broadcast(prefix) {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("gateway must be in the same subnet as the static IP")
	}

	return prefix, gw, nil
}

func subnetMaskToPrefix(subnetMask string) (int, error) {
	mask, err := netip.ParseAddr(strings.TrimSpace(subnetMask))
	if err != nil || !mask.Is4() {
		return 0, fmt.Errorf("invalid subnet mask")
	}

	bytes := mask.As4()
	value := binary.BigEndian.Uint32(bytes[:])
	prefix := 0
	seenZero := false
	for bit := 31; bit >= 0; bit-- {
		isOne := value&(uint32(1)<<bit) != 0
		if isOne {
			if seenZero {
				return 0, fmt.Errorf("subnet mask must be contiguous")
			}
			prefix++
		} else {
			seenZero = true
		}
	}
	if prefix < 1 || prefix > 30 {
		return 0, fmt.Errorf("subnet mask must be between 255.0.0.0 and 255.255.255.252")
	}

	return prefix, nil
}

func prefixToSubnetMask(prefix int) string {
	value := ^uint32(0) << (32 - prefix)
	var mask [4]byte
	binary.BigEndian.PutUint32(mask[:], value)
	return netip.AddrFrom4(mask).String()
}

func ipv4Broadcast(prefix netip.Prefix) netip.Addr {
	address := prefix.Masked().Addr().As4()
	value := binary.BigEndian.Uint32(address[:])
	value |= (uint32(1) << (32 - prefix.Bits())) - 1
	var broadcast [4]byte
	binary.BigEndian.PutUint32(broadcast[:], value)
	return netip.AddrFrom4(broadcast)
}

func restartEthernetAfterResponse() {
	time.Sleep(time.Second)
	if err := exec.Command(ethernetInitScript, "restart").Run(); err != nil {
		log.Errorf("failed to restart ethernet: %s", err)
	}
}
