package network

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/atomicfile"
	"NanoKVM-Server/proto"
)

// WolMacFile stores the Wake-on-LAN history (a variable so tests can redirect it).
var WolMacFile = "/etc/kvm/cache/wol"

// maxWolHistory bounds the remembered MAC addresses.
const maxWolHistory = 100

// wolMacMutex serialises every read-modify-write of WolMacFile; concurrent
// wakes, renames and deletes used to overwrite each other's changes.
var wolMacMutex sync.Mutex

// Any user may wake, and every wake runs ether-wake and broadcasts on the
// LAN: one a second on average, five at once.
var wolLimiter = &wakeLimiter{burst: 5, every: time.Second}

// Wakes add new addresses to the history in memory, and the file is
// rewritten at most once per wolFlushInterval: a loop of wakes with fresh
// addresses would otherwise be a stream of fsync'd SD card writes. An
// administrator's rename or delete writes them at once.
var (
	wolFlushInterval = time.Minute
	wolPending       []string // under wolMacMutex
	wolFlushTimer    *time.Timer
)

type wakeLimiter struct {
	mu     sync.Mutex
	burst  float64
	every  time.Duration
	tokens float64
	last   time.Time
}

func (l *wakeLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last.IsZero() {
		l.tokens = l.burst
	} else {
		l.tokens = min(l.burst, l.tokens+float64(now.Sub(l.last))/float64(l.every))
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

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

	if err := Wake(req.Mac, req.Interface); err != nil {
		var wakeErr *WakeError
		if errors.As(err, &wakeErr) {
			rsp.ErrRsp(c, wakeErr.Code, wakeErr.Message)
		} else {
			rsp.ErrRsp(c, -3, err.Error())
		}
		return
	}
	rsp.OkRsp(c)
}

// WakeError is a refused or failed wake with its API code.
type WakeError struct {
	Code    int
	Message string
}

func (e *WakeError) Error() string { return e.Message }

// Wake sends a magic packet to mac through iface ("" picks one), with the
// web page's checks and rate limit, and remembers the address.
func Wake(macAddress, iface string) error {
	mac, err := parseMAC(macAddress)
	if err != nil {
		return &WakeError{-2, "invalid MAC address"}
	}

	interfaceName, err := selectWolInterface(iface)
	if err != nil {
		return &WakeError{-2, "invalid network interface"}
	}

	if !wolLimiter.allow(time.Now()) {
		return &WakeError{-4, "too many wake requests, try again in a second"}
	}

	cmd := wolCommand(interfaceName, mac)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Errorf("failed to wake on lan: %s", err)
		return &WakeError{-3, string(output)}
	}

	saveMac(mac)
	log.Debugf("wake on lan: %s via %s", mac, interfaceName)
	return nil
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

	wolMacMutex.Lock()
	macs, err := readWolMacs()
	if errors.Is(err, os.ErrNotExist) {
		macs, err = []string{}, nil
	}
	if err == nil {
		// The addresses not written yet are history too.
		macs = boundWolHistory(append(macs, wolPending...))
	}
	wolMacMutex.Unlock()
	if err != nil {
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

	wolMacMutex.Lock()
	defer wolMacMutex.Unlock()
	flushWolPendingLocked()
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

	wolMacMutex.Lock()
	defer wolMacMutex.Unlock()
	flushWolPendingLocked()
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
	wolMacMutex.Lock()
	defer wolMacMutex.Unlock()

	if isMacExist(mac) || slices.Contains(wolPending, mac) {
		return
	}
	wolPending = append(wolPending, mac)
	if wolFlushTimer == nil {
		wolFlushTimer = time.AfterFunc(wolFlushInterval, func() {
			wolMacMutex.Lock()
			defer wolMacMutex.Unlock()
			flushWolPendingLocked()
		})
	}
}

// flushWolPendingLocked writes the addresses wakes added since the last
// write, in one rewrite of the file.
func flushWolPendingLocked() {
	if wolFlushTimer != nil {
		wolFlushTimer.Stop()
		wolFlushTimer = nil
	}
	if len(wolPending) == 0 {
		return
	}
	pending := wolPending
	wolPending = nil

	err := os.MkdirAll(filepath.Dir(WolMacFile), 0o755)
	if err != nil {
		log.Errorf("failed to create dir: %s", err)
		return
	}

	macs, err := readWolMacs()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Errorf("failed to read %s: %s", WolMacFile, err)
		return
	}
	if err = writeWolMacs(boundWolHistory(append(macs, pending...))); err != nil {
		log.Errorf("failed to write %s: %s", WolMacFile, err)
	}
}

// boundWolHistory drops the oldest unnamed entry (or the oldest entry when
// all of them are named) until maxWolHistory remain: any user can wake.
func boundWolHistory(macs []string) []string {
	for len(macs) > maxWolHistory {
		drop := 0
		for i, line := range macs {
			if _, name, ok := splitWolMacLine(line); ok && name == "" {
				drop = i
				break
			}
		}
		macs = append(macs[:drop], macs[drop+1:]...)
	}
	return macs
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

	return atomicfile.Write(WolMacFile, []byte(data), 0o644)
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
