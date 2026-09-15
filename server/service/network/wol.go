package network

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
)

const (
	WolMacFile = "/etc/kvm/cache/wol"
)

var systemNetworkInterfaces = net.Interfaces
var wolSysClassNet = "/sys/class/net"

func wolInterfaceIsHardware(name string) bool {
	base := filepath.Join(wolSysClassNet, name)
	if _, err := os.Stat(filepath.Join(base, "device")); err != nil {
		return false
	}

	uevent, err := os.ReadFile(filepath.Join(base, "uevent"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(uevent), "\n") {
		if line == "DEVTYPE=gadget" {
			return false
		}
	}
	return true
}

func (s *Service) WakeOnLAN(c *gin.Context) {
	var req proto.WakeOnLANReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	mac, err := parseMAC(req.Mac)
	if err != nil {
		rsp.ErrRsp(c, -2, "invalid MAC address")
		return
	}

	interfaceName, err := selectWolInterface(req.Interface)
	if err != nil {
		rsp.ErrRsp(c, -2, "invalid network interface")
		return
	}

	cmd := wolCommand(interfaceName, mac)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Errorf("failed to wake on lan: %s", err)
		rsp.ErrRsp(c, -3, string(output))
		return
	}

	saveMac(mac)

	rsp.OkRsp(c)
	log.Debugf("wake on lan: %s via %s", mac, interfaceName)
}

func (s *Service) GetWolInterfaces(c *gin.Context) {
	var rsp proto.Response

	interfaces, err := wolInterfaces()
	if err != nil {
		log.Errorf("failed to list wake-on-lan interfaces: %s", err)
		rsp.ErrRsp(c, -2, "list interfaces failed")
		return
	}

	rsp.OkRspWithData(c, &proto.GetWolInterfacesRsp{Interfaces: interfaces})
}

func wolInterfaces() ([]string, error) {
	interfaces, err := systemNetworkInterfaces()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(interfaces))
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 ||
			item.Flags&net.FlagBroadcast == 0 || len(item.HardwareAddr) != 6 ||
			!wolInterfaceIsHardware(item.Name) {
			continue
		}
		names = append(names, item.Name)
	}
	sort.Strings(names)
	return names, nil
}

func selectWolInterface(requested string) (string, error) {
	interfaces, err := wolInterfaces()
	if err != nil {
		return "", err
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		for _, name := range interfaces {
			if name == "eth0" {
				return name, nil
			}
		}
		if len(interfaces) > 0 {
			return interfaces[0], nil
		}
		return "", errors.New("no wake-on-lan interface is available")
	}
	for _, name := range interfaces {
		if requested == name {
			return name, nil
		}
	}
	return "", fmt.Errorf("network interface %q is not available for wake-on-lan", requested)
}

func wolCommand(interfaceName, mac string) *exec.Cmd {
	return exec.Command("ether-wake", "-i", interfaceName, "-b", mac)
}

func (s *Service) GetMac(c *gin.Context) {
	var rsp proto.Response

	macs, err := readWolMacs()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			rsp.OkRspWithData(c, &proto.GetMacRsp{Macs: []string{}})
			return
		}

		rsp.ErrRsp(c, -2, "open file error")
		return
	}

	data := &proto.GetMacRsp{
		Macs: macs,
	}

	rsp.OkRspWithData(c, data)
}

func (s *Service) SetMacName(c *gin.Context) {
	var req proto.SetMacNameReq // Mac:string Name:string
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	mac, err := parseMAC(req.Mac)
	if err != nil {
		rsp.ErrRsp(c, -2, "invalid MAC address")
		return
	}

	name := sanitizeWolMacName(req.Name)
	if name == "" {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	macs, err := readWolMacs()
	if err != nil {
		log.Errorf("failed to open %s: %s", WolMacFile, err)
		rsp.ErrRsp(c, -2, "read failed")
		return
	}

	var newLines []string
	macFound := false

	for _, line := range macs {
		itemMac, itemName, ok := splitWolMacLine(line)
		if !ok {
			continue
		}
		normalizedMac, err := parseMAC(itemMac)
		if err != nil {
			newLines = append(newLines, formatWolMacLine(itemMac, itemName))
			continue
		}
		if mac != normalizedMac {
			newLines = append(newLines, formatWolMacLine(normalizedMac, itemName))
			continue
		}

		newLines = append(newLines, formatWolMacLine(normalizedMac, name))
		macFound = true
	}

	if !macFound {
		log.Errorf("failed to find mac %s", req.Mac)
		rsp.ErrRsp(c, -3, "write failed")
		return
	}

	if err = writeWolMacs(newLines); err != nil {
		log.Errorf("failed to write %s: %s", WolMacFile, err)
		rsp.ErrRsp(c, -3, "write failed")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("set wol mac name: %s %s", req.Mac, req.Name)
}

func (s *Service) DeleteMac(c *gin.Context) {
	var req proto.DeleteMacReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	mac, err := parseMAC(req.Mac)
	if err != nil {
		rsp.ErrRsp(c, -2, "invalid MAC address")
		return
	}

	macs, err := readWolMacs()
	if err != nil {
		log.Errorf("failed to open %s: %s", WolMacFile, err)
		rsp.ErrRsp(c, -2, "read failed")
		return
	}

	var newMacs []string

	for _, line := range macs {
		itemMac, itemName, ok := splitWolMacLine(line)
		if !ok {
			continue
		}
		normalizedMac, err := parseMAC(itemMac)
		if err != nil {
			newMacs = append(newMacs, formatWolMacLine(itemMac, itemName))
			continue
		}
		if mac != normalizedMac {
			newMacs = append(newMacs, formatWolMacLine(normalizedMac, itemName))
		}
	}

	if err = writeWolMacs(newMacs); err != nil {
		log.Errorf("failed to write %s: %s", WolMacFile, err)
		rsp.ErrRsp(c, -3, "write failed")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("delete wol mac: %s", req.Mac)
}

func parseMAC(mac string) (string, error) {
	mac = strings.ToUpper(strings.TrimSpace(mac))

	mac = strings.ReplaceAll(mac, "-", "")
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, ".", "")

	matched, err := regexp.MatchString("^[0-9A-F]{12}$", mac)
	if err != nil {
		return "", err
	}
	if !matched {
		return "", fmt.Errorf("invalid MAC address: %s", mac)
	}

	var result strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			result.WriteString(":")
		}
		result.WriteString(mac[i : i+2])
	}

	return result.String(), nil
}

func saveMac(mac string) {
	if isMacExist(mac) {
		return
	}

	err := os.MkdirAll(filepath.Dir(WolMacFile), 0o755)
	if err != nil {
		log.Errorf("failed to create dir: %s", err)
		return
	}

	file, err := os.OpenFile(WolMacFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Errorf("failed to open %s: %s", WolMacFile, err)
		return
	}
	defer func() {
		_ = file.Close()
	}()

	content := fmt.Sprintf("%s\n", mac)
	_, err = file.WriteString(content)
	if err != nil {
		log.Errorf("failed to write %s: %s", WolMacFile, err)
		return
	}
}

func isMacExist(mac string) bool {
	macs, err := readWolMacs()
	if err != nil {
		return false
	}

	for _, item := range macs {
		itemMac, _, ok := splitWolMacLine(item)
		if !ok {
			continue
		}
		normalizedMac, err := parseMAC(itemMac)
		if err != nil {
			continue
		}
		if mac == normalizedMac {
			return true
		}
	}

	return false
}

func readWolMacs() ([]string, error) {
	content, err := os.ReadFile(WolMacFile)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(content), "\n")
	macs := make([]string, 0, len(lines))
	for _, line := range lines {
		mac, name, ok := splitWolMacLine(line)
		if !ok {
			continue
		}

		macs = append(macs, formatWolMacLine(mac, name))
	}

	return macs, nil
}

func writeWolMacs(macs []string) error {
	data := ""
	if len(macs) > 0 {
		data = strings.Join(macs, "\n") + "\n"
	}

	return os.WriteFile(WolMacFile, []byte(data), 0o644)
}

func splitWolMacLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", "", false
	}

	mac := fields[0]
	if len(line) == len(mac) {
		return mac, "", true
	}

	name := line[len(mac):]
	return mac, strings.TrimSpace(name), true
}

func formatWolMacLine(mac string, name string) string {
	if name == "" {
		return mac
	}

	return mac + " " + name
}

func sanitizeWolMacName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}
