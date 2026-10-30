package standby

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func snapshotNetwork(c *Config) error {
	ip := net.ParseIP(c.Address)
	if ip == nil || ip.To4() == nil {
		return errors.New("open NanoKVM at its direct Ethernet or Wi-Fi IPv4 address first")
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	found := false
	for _, nic := range ifaces {
		addrs, e := nic.Addrs()
		if e != nil {
			return e
		}
		for _, addr := range addrs {
			a, subnet, e := net.ParseCIDR(addr.String())
			if e != nil || !a.Equal(ip) {
				continue
			}
			if nic.Name != "eth0" && nic.Name != "wlan0" {
				return errors.New("standby does not support USB, VPN, VLAN or bridge management; use eth0 or station Wi-Fi")
			}
			if _, e = os.Stat("/sys/class/net/" + nic.Name + "/master"); !errors.Is(e, os.ErrNotExist) {
				return errors.New("bridged management is unsupported")
			}
			if nic.Flags&net.FlagUp == 0 || len(nic.HardwareAddr) != 6 {
				return errors.New("management interface is not ready")
			}
			c.Interface, c.MAC = nic.Name, nic.HardwareAddr.String()
			c.Prefix, _ = subnet.Mask.Size()
			c.WiFi = nic.Name == "wlan0"
			found = true
		}
	}
	if !found {
		return errors.New("use the device's direct IPv4 address; proxy/VPN and hostname handoff is unsupported")
	}
	if c.WiFi {
		if _, e := os.Stat("/tmp/wifiap"); !errors.Is(e, os.ErrNotExist) {
			return errors.New("Wi-Fi access-point standby is unsupported; join a Wi-Fi network or use Ethernet")
		}
	}
	pidFile := "/run/udhcpc." + c.Interface + ".pid"
	if b, e := os.ReadFile(pidFile); e == nil {
		pid, e := strconv.Atoi(strings.TrimSpace(string(b)))
		if e != nil || pid < 2 {
			return errors.New("invalid DHCP state")
		}
		cmd, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		args := strings.Split(string(cmd), "\x00")
		matched := false
		for i := 1; i < len(args); i++ {
			if args[i-1] == "-i" && args[i] == c.Interface {
				matched = true
			}
		}
		if e != nil || !strings.Contains(args[0], "udhcpc") || !matched {
			return errors.New("DHCP state is not current")
		}
		c.DHCP = true
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if !c.DHCP {
		if c.WiFi {
			return errors.New("station Wi-Fi without the managed DHCP client is unsupported")
		}
		b, e := os.ReadFile("/boot/eth.nodhcp")
		if e != nil {
			return errors.New("cannot confirm static Ethernet configuration")
		}
		matched := false
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(strings.SplitN(line, "#", 2)[0])
			if len(fields) > 0 && strings.SplitN(fields[0], "/", 2)[0] == c.Address {
				matched = true
			}
		}
		if !matched {
			return errors.New("management address does not match saved static Ethernet configuration")
		}
	}
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return err
	}
	if route, e := clientRoute(string(b), c.ClientAddress); e != nil {
		return e
	} else if route != c.Interface {
		return errors.New("connect through the selected Ethernet or Wi-Fi network before shutdown; VPN/proxy ingress is unsupported")
	}
	if err = simpleRoutes(string(b), *c); err != nil {
		return err
	}
	c.Gateway, err = defaultGateway(string(b), c.Interface)
	if err != nil {
		return err
	}
	if b, e := os.ReadFile("/etc/hostname"); e == nil {
		c.Hostname = strings.TrimSpace(string(b))
	}
	return nil
}
func defaultGateway(table, iface string) (string, error) {
	gateway := ""
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[0] != iface || f[1] != "00000000" {
			continue
		}
		if gateway != "" {
			return "", errors.New("multiple management default routes are unsupported")
		}
		n, e := strconv.ParseUint(f[2], 16, 32)
		if e != nil {
			return "", e
		}
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(n))
		gateway = net.IP(b).String()
	}
	if gateway == "0.0.0.0" {
		gateway = ""
	}
	return gateway, nil
}
func ramWPA(data []byte) ([]byte, error) {
	// The managed PSK/SAE/open network format is self-contained. Reject custom
	// EAP certificate paths, includes and loadable engines instead of silently
	// depending on the removed root.
	allowed := map[string]bool{"ssid": true, "psk": true, "sae_password": true, "key_mgmt": true,
		"scan_ssid": true, "id_str": true, "priority": true, "disabled": true, "bgscan": true,
		"ieee80211w": true, "sae_pwe": true, "proto": true, "pairwise": true, "group": true,
		"freq_list": true, "country": true, "ap_scan": true}
	out := []string{"ctrl_interface=/run/wpa_supplicant", "update_config=0"}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "network={" || line == "}" {
			out = append(out, line)
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if key == "ctrl_interface" || key == "update_config" {
			continue
		}
		if !ok || !allowed[key] {
			return nil, fmt.Errorf("unsupported Wi-Fi standby setting %q", key)
		}
		out = append(out, line)
	}
	if !strings.Contains(string(data), "network={") {
		return nil, errors.New("no station Wi-Fi configuration")
	}
	return []byte(strings.Join(out, "\n") + "\n"), nil
}

func busybox(args ...string) error {
	out, err := exec.Command("/bin/busybox", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("network %s: %s: %w", args[0], out, err)
	}
	return nil
}
func startNetwork(c Config, failed func(error)) error {
	if err := busybox("ip", "link", "set", "lo", "up"); err != nil {
		return err
	}
	if err := busybox("ip", "link", "set", c.Interface, "up"); err != nil {
		return err
	}
	start := func(path string, args ...string) error {
		cmd := exec.Command(path, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { err := cmd.Wait(); failed(fmt.Errorf("%s stopped: %v", path, err)) }()
		return nil
	}
	if c.WiFi {
		if err := start("/sbin/wpa_supplicant", "-Dnl80211", "-i"+c.Interface, "-c/wpa.conf"); err != nil {
			return err
		}
	}
	if !c.DHCP {
		if err := busybox("ip", "addr", "replace", fmt.Sprintf("%s/%d", c.Address, c.Prefix), "dev", c.Interface); err != nil {
			return err
		}
		if c.Gateway != "" {
			return busybox("ip", "route", "replace", "default", "via", c.Gateway, "dev", c.Interface)
		}
		return nil
	}
	args := []string{"udhcpc", "-f", "-B", "-i", c.Interface, "-r", c.Address, "-s", "/init", "-p", "/run/udhcpc.pid", "-t", "5", "-T", "2", "-A", "5"}
	if c.Hostname != "" {
		args = append(args, "-x", "hostname:"+c.Hostname)
	}
	if err := start("/bin/busybox", args...); err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if b, e := os.ReadFile("/run/network.ready"); e == nil && string(b) == c.Address {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("DHCP did not restore the management address; use the router lease list and the saved restart key")
}

// DHCPHook is called by the staged udhcpc. It applies only this client's
// interface and stores no lease or DNS state outside RAM. A changed address is
// valid, but prevents publishing ready at an address the browser may have lost.
func DHCPHook(event string) error {
	c, err := LoadConfig("/config.json")
	if err != nil {
		return err
	}
	if os.Getenv("interface") != c.Interface {
		return errors.New("unexpected DHCP interface")
	}
	switch event {
	case "deconfig", "nak", "leasefail":
		os.Remove("/run/network.ready")
		return busybox("ip", "-4", "addr", "flush", "dev", c.Interface)
	case "bound", "renew":
		ip := net.ParseIP(os.Getenv("ip")).To4()
		mask := net.ParseIP(os.Getenv("subnet")).To4()
		if ip == nil || mask == nil {
			return errors.New("invalid DHCP address")
		}
		prefix, bits := net.IPMask(mask).Size()
		if bits != 32 {
			return errors.New("invalid DHCP subnet")
		}
		if err = busybox("ip", "-4", "addr", "flush", "dev", c.Interface); err != nil {
			return err
		}
		if err = busybox("ip", "route", "flush", "dev", c.Interface); err != nil {
			return err
		}
		if err = busybox("ip", "addr", "replace", fmt.Sprintf("%s/%d", ip, prefix), "dev", c.Interface); err != nil {
			return err
		}
		routers := strings.Fields(os.Getenv("router"))
		if len(routers) > 0 {
			gw := net.ParseIP(routers[0]).To4()
			if gw == nil {
				return errors.New("invalid DHCP gateway")
			}
			if err = busybox("ip", "route", "replace", "default", "via", gw.String(), "dev", c.Interface); err != nil {
				return err
			}
		}
		return os.WriteFile("/run/network.ready", []byte(ip.String()), 0600)
	default:
		return nil
	}
}

// Use the actual TCP peer, never a forwarded header. A tunnel may address
// eth0's IP but depend on a VPN route that vanishes when its service stops.
func clientRoute(table, address string) (string, error) {
	ip := net.ParseIP(address).To4()
	if ip == nil || ip.IsLoopback() {
		return "", errors.New("direct IPv4 management is required")
	}
	target := binary.LittleEndian.Uint32(ip)
	bestBits, bestMetric, best := -1, uint64(^uint32(0)), ""
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[0] == "Iface" {
			continue
		}
		dest, e1 := strconv.ParseUint(f[1], 16, 32)
		mask, e2 := strconv.ParseUint(f[7], 16, 32)
		metric, e3 := strconv.ParseUint(f[6], 10, 32)
		flags, e4 := strconv.ParseUint(f[3], 16, 32)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			return "", errors.New("unrecognized routing table")
		}
		if flags&1 == 0 || uint64(target)&mask != dest {
			continue
		}
		mb := make([]byte, 4)
		binary.LittleEndian.PutUint32(mb, uint32(mask))
		bits, n := net.IPMask(mb).Size()
		if n != 32 {
			return "", errors.New("invalid route mask")
		}
		if bits > bestBits || bits == bestBits && metric < bestMetric {
			bestBits, bestMetric, best = bits, metric, f[0]
		}
	}
	if best == "" {
		return "", errors.New("no return route for the management client")
	}
	return best, nil
}

// Only connected-subnet and default routes are reconstructed. Reject classless
// DHCP routes/custom topology before stopping the existing network service.
func simpleRoutes(table string, c Config) error {
	ip := net.ParseIP(c.Address).To4()
	if ip == nil || c.Prefix < 0 || c.Prefix > 32 {
		return errors.New("invalid management subnet")
	}
	mask := binary.LittleEndian.Uint32(net.CIDRMask(c.Prefix, 32))
	subnet := binary.LittleEndian.Uint32(ip) & mask
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[0] != c.Interface {
			continue
		}
		dest, e1 := strconv.ParseUint(f[1], 16, 32)
		gateway, e2 := strconv.ParseUint(f[2], 16, 32)
		routeMask, e3 := strconv.ParseUint(f[7], 16, 32)
		if e1 != nil || e2 != nil || e3 != nil {
			return errors.New("invalid management route")
		}
		if dest == 0 && routeMask == 0 {
			continue
		}
		if uint32(dest) == subnet && uint32(routeMask) == mask && gateway == 0 {
			continue
		}
		return errors.New("custom/classless routes on the management interface are unsupported for standby")
	}
	return nil
}
