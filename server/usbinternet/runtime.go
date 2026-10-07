package usbinternet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Runtime struct{ Boot, Run, Gadget, Net, Proc, Resolv string }

func DefaultRuntime() Runtime {
	return Runtime{Boot: env("NANOKVM_USB_NET_BOOT", "/boot"), Run: env("NANOKVM_USB_NET_RUN", "/run/nanokvm-usbnet"), Gadget: env("NANOKVM_USB_NET_GADGET", "/sys/kernel/config/usb_gadget/g0"), Net: env("NANOKVM_USB_NET_CLASS", "/sys/class/net"), Proc: env("NANOKVM_USB_NET_PROC", "/proc"), Resolv: env("NANOKVM_USB_NET_RESOLV", "/etc/resolv.conf")}
}
func env(k, v string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return v
}
func exists(p string) bool      { _, err := os.Stat(p); return err == nil }
func (r Runtime) Enabled() bool { return exists(filepath.Join(r.Boot, "usb.internet")) }
func (r Runtime) command(input, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", name, err)
	}
	return out, nil
}
func atomicFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".usb-internet-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (r Runtime) save(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(r.Run, name), append(data, '\n'))
}
func (r Runtime) status(s Status) error {
	s.UpdatedAt = time.Now().Unix()
	return r.save("status.json", s)
}
func (r Runtime) ReadStatus() Status {
	s := Status{Enabled: r.Enabled(), State: "disabled"}
	data, err := os.ReadFile(filepath.Join(r.Run, "status.json"))
	if err != nil || json.Unmarshal(data, &s) != nil || time.Now().Unix()-s.UpdatedAt > 20 {
		s = Status{State: "unavailable"}
	}
	s.Enabled = r.Enabled()
	s.IPv6 = false
	return s
}

type applied struct {
	FlowUnsupported                                 bool
	AddressOwned                                    bool
	Prefix, Address, Uplink, Config, Rules, Routing string
	Forward                                         map[string]string
}

func (r Runtime) load() (applied, error) {
	var a applied
	data, err := os.ReadFile(filepath.Join(r.Run, "applied.json"))
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(data, &a)
	return a, err
}
func (r Runtime) bound() bool {
	udc, err := os.ReadFile(filepath.Join(r.Gadget, "UDC"))
	if err != nil || strings.TrimSpace(string(udc)) == "" {
		return false
	}
	for _, name := range []string{"ncm.usb0", "rndis.usb0"} {
		if f, err := os.Lstat(filepath.Join(r.Gadget, "configs/c.1", name)); err == nil && f.Mode()&os.ModeSymlink != 0 {
			return exists(filepath.Join(r.Net, "usb0"))
		}
	}
	return false
}
func (r Runtime) occupied() ([]netip.Prefix, error) {
	data, err := r.command("", "ip", "-j", "-4", "route", "show", "table", "all")
	if err != nil {
		return nil, err
	}
	var routes []route
	if err = json.Unmarshal(data, &routes); err != nil {
		return nil, err
	}
	var occupied []netip.Prefix
	for _, rt := range routes {
		if rt.Dev == "usb0" || rt.Dst == "default" {
			continue
		}
		if p, err := netip.ParsePrefix(rt.Dst); err == nil {
			occupied = append(occupied, p)
		} else if ip, err := netip.ParseAddr(rt.Dst); err == nil {
			occupied = append(occupied, netip.PrefixFrom(ip, 32))
		}
	}
	return occupied, nil
}
func (r Runtime) uplink(subnet netip.Prefix) (string, string) {
	// Include ingress and source in the lookup so policy routing applies to the
	// managed host. Never select a VPN/tunnel or bypass it with a new routing table.
	data, err := r.command("", "ip", "-j", "-4", "route", "get", "1.1.1.1", "from", subnetPrefix(subnet)+".100", "iif", "usb0")
	if err != nil {
		return "", ""
	}
	var routes []route
	if json.Unmarshal(data, &routes) != nil || len(routes) != 1 {
		return "", ""
	}
	rt := routes[0]
	if rt.Dev != "eth0" && rt.Dev != "wlan0" {
		return "", ""
	}
	if rt.Type != "" && rt.Type != "unicast" {
		return "", ""
	}
	// A route on an unplugged cable is not a usable uplink.
	carrier, err := os.ReadFile(filepath.Join(r.Net, rt.Dev, "carrier"))
	if err != nil || strings.TrimSpace(string(carrier)) != "1" {
		return "", ""
	}
	return rt.Dev, string(data)
}
func (r Runtime) ownedDaemon() (int, bool) {
	data, err := os.ReadFile(filepath.Join(r.Run, "dnsmasq.pid"))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return 0, false
	}
	path := filepath.Join(r.Proc, strconv.Itoa(pid))
	comm, err := os.ReadFile(filepath.Join(path, "comm"))
	if err != nil || strings.TrimSpace(string(comm)) != "dnsmasq" {
		return pid, false
	}
	args, err := os.ReadFile(filepath.Join(path, "cmdline"))
	if err != nil {
		return pid, false
	}
	for _, arg := range strings.Split(string(args), "\x00") {
		if arg == "--conf-file="+filepath.Join(r.Run, "dnsmasq.conf") {
			return pid, true
		}
	}
	return pid, false
}
func (r Runtime) stopDaemon() error {
	if pid, owned := r.ownedDaemon(); owned {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return err
		}
		for n := 0; n < 20; n++ {
			if _, owned = r.ownedDaemon(); !owned {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if _, owned = r.ownedDaemon(); owned {
			return errors.New("USB DNS/DHCP did not stop")
		}
	}
	if err := os.Remove(filepath.Join(r.Run, "dnsmasq.pid")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
func (r Runtime) daemon(config string) error {
	path := filepath.Join(r.Run, "dnsmasq.conf")
	old, _ := os.ReadFile(path)
	if _, owned := r.ownedDaemon(); owned && string(old) == config {
		return nil
	}
	if err := r.stopDaemon(); err != nil {
		return err
	}
	if err := atomicFile(path, []byte(config)); err != nil {
		return err
	}
	if _, err := r.command("", "dnsmasq", "--test", "--conf-file="+path); err != nil {
		return err
	}
	cmd := exec.Command("dnsmasq", "--keep-in-foreground", "--conf-file="+path)
	log, err := os.OpenFile(filepath.Join(r.Run, "dnsmasq.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd.Stderr = log
	cmd.Stdout = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := atomicFile(filepath.Join(r.Run, "dnsmasq.pid"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n")); err != nil {
		cmd.Process.Kill()
		return err
	}
	go cmd.Wait()
	// Bind/privilege errors happen after exec. Require a running owned process.
	time.Sleep(100 * time.Millisecond)
	if _, owned := r.ownedDaemon(); !owned {
		return errors.New("USB DNS/DHCP failed to start")
	}
	return nil
}
func (r Runtime) sysctlPath(dev string) string {
	return filepath.Join(r.Proc, "sys/net/ipv4/conf", dev, "forwarding")
}
func (r Runtime) forwarding(a *applied, uplink string, foreign bool) error {
	wanted := map[string]bool{}
	if uplink != "" {
		wanted["usb0"] = true
		wanted[uplink] = true
	}
	if a.Forward == nil {
		a.Forward = map[string]string{}
	}
	for dev, original := range a.Forward {
		if wanted[dev] {
			continue
		}
		// If another forwarding policy appeared while sharing was active, keep its
		// forwarding available. Never toggle global ip_forward (it resets sysctls).
		current, err := os.ReadFile(r.sysctlPath(dev))
		if err == nil && strings.TrimSpace(string(current)) == "1" && !foreign {
			if err = os.WriteFile(r.sysctlPath(dev), []byte(original), 0600); err != nil {
				return err
			}
		}
		delete(a.Forward, dev)
	}
	for dev := range wanted {
		if err := r.ensureForward(a, dev); err != nil {
			return err
		}
	}
	return nil
}

func (r Runtime) ensureForward(a *applied, dev string) error {
	data, err := os.ReadFile(r.sysctlPath(dev))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "1" {
		return nil
	}
	if a.Forward == nil {
		a.Forward = map[string]string{}
	}
	if _, owned := a.Forward[dev]; !owned {
		a.Forward[dev] = string(data)
	}
	if err = r.save("applied.json", a); err != nil {
		return err
	}
	return os.WriteFile(r.sysctlPath(dev), []byte("1\n"), 0600)
}

func (r Runtime) Apply(preferred string, stop bool) (err error) {
	if err = os.MkdirAll(r.Run, 0700); err != nil {
		return err
	}
	// Independent API, gadget and watcher entry points share this lock too.
	lock, err := os.OpenFile(filepath.Join(r.Run, "internet.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	a, err := r.load()
	if err != nil {
		return err
	}
	s := Status{Enabled: r.Enabled(), State: "disabled"}
	defer func() {
		if err != nil {
			s.State = "error"
		}
		if e := r.status(s); err == nil {
			err = e
		}
	}()
	data, err := r.command("", "nft", "-j", "list", "ruleset")
	if err != nil {
		return err
	}
	tableExists, foreign, err := inspectRules(data)
	if err != nil {
		return err
	}
	defer func() {
		if err == nil || a.Address == "" {
			return
		}
		// On a runtime error remove the stale gateway/NAT while retaining the
		// USB address. Check ownership again before replacing any nft table.
		latest, e := r.command("", "nft", "-j", "list", "ruleset")
		owned, transit, check := inspectRules(latest)
		if e == nil && check == nil {
			if p, parse := netip.ParsePrefix(a.Prefix + ".0/24"); parse == nil {
				guard := ruleset(p, "", false, owned)
				if _, e = r.command(guard, "nft", "-f", "-"); e == nil {
					_ = r.forwarding(&a, "", transit)
					_ = r.daemon(dnsConfig(r.Resolv, a.Prefix, false))
					a.Rules = ruleset(p, "", false, false)
					a.Uplink = ""
					a.Routing = ""
					_ = r.save("applied.json", a)
					return
				}
			}
		}
		_ = r.stopDaemon()
	}()

	cleanup := func() error {
		// Delete only the table with our verified owner comment. No flush ruleset.
		if tableExists {
			if _, e := r.command("", "nft", "delete", "table", "inet", Table); e != nil {
				return e
			}
			tableExists = false
		}
		if e := r.stopDaemon(); e != nil {
			return e
		}
		if e := r.forwarding(&a, "", foreign); e != nil {
			return e
		}
		if a.Address != "" && a.AddressOwned {
			if exists(filepath.Join(r.Net, "usb0")) {
				if _, e := r.command("", "ip", "address", "del", a.Address, "dev", "usb0"); e != nil {
					// Missing address after gadget rebind is harmless; other failures are not.
					addresses, qerr := r.command("", "ip", "-j", "-4", "address", "show", "dev", "usb0")
					if qerr != nil || strings.Contains(string(addresses), strings.Split(a.Address, "/")[0]) {
						return e
					}
				}
			}
		}
		a.Address = ""
		a.AddressOwned = false
		a.Uplink = ""
		a.Routing = ""
		a.Config = ""
		a.Rules = ""
		return r.save("applied.json", a)
	}
	if stop || !r.bound() || exists(filepath.Join(r.Boot, "rndis.nodhcpd")) {
		if s.Enabled {
			s.State = "waiting-ncm"
			if exists(filepath.Join(r.Boot, "rndis.nodhcpd")) {
				s.State = "dhcp-disabled"
			}
		}
		return cleanup()
	}
	occupied, err := r.occupied()
	if err != nil {
		return err
	}
	custom := exists(filepath.Join(r.Boot, "rndis.ipv4_prefix"))
	if a.Prefix != "" && !custom {
		preferred = a.Prefix
	}
	subnet, err := chooseSubnet(preferred, custom, occupied)
	if err != nil {
		s.State = "subnet-conflict"
		// Preserve local access; only retire sharing and its gateway advertisement.
		if a.Address == "" {
			err = nil
			return cleanup()
		}
		oldSubnet, e := netip.ParsePrefix(a.Prefix + ".0/24")
		if e != nil {
			return e
		}
		guard := ruleset(oldSubnet, "", false, tableExists)
		if _, e = r.command(guard, "nft", "-f", "-"); e != nil {
			return e
		}
		if e = r.forwarding(&a, "", foreign); e != nil {
			return e
		}
		if e = r.daemon(dnsConfig(r.Resolv, a.Prefix, false)); e != nil {
			return e
		}
		a.Uplink = ""
		a.Rules = ""
		return r.save("applied.json", a)
	}
	prefix := subnetPrefix(subnet)
	addresses, err := r.command("", "ip", "-j", "address", "show", "dev", "usb0")
	if err != nil {
		return err
	}
	var links []struct {
		Flags     []string `json:"flags"`
		Addresses []struct {
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err = json.Unmarshal(addresses, &links); err != nil || len(links) != 1 {
		return errors.New("cannot inspect USB interface")
	}
	up := false
	for _, flag := range links[0].Flags {
		if flag == "UP" {
			up = true
		}
	}
	if !up {
		if _, err = r.command("", "ip", "link", "set", "dev", "usb0", "up"); err != nil {
			return err
		}
	}
	address := prefix + ".1/24"
	if a.Address != "" && a.Address != address && a.AddressOwned {
		if err = r.stopDaemon(); err != nil {
			return err
		}
		if _, err = r.command("", "ip", "address", "del", a.Address, "dev", "usb0"); err != nil {
			return err
		}
	}

	addressPresent := false
	for _, existing := range links[0].Addresses {
		if existing.Local == prefix+".1" && existing.PrefixLen == 24 {
			addressPresent = true
		}
	}
	owned := a.Address == address && a.AddressOwned
	if !addressPresent {
		owned = true
	}
	a.Prefix = prefix
	a.Address = address
	a.AddressOwned = owned
	if err = r.save("applied.json", a); err != nil {
		return err
	}
	if !addressPresent {
		if _, err = r.command("", "ip", "address", "replace", address, "dev", "usb0"); err != nil {
			return err
		}
	}

	s.Address = address
	uplink := ""
	routing := ""
	flow := false
	if s.Enabled {
		// The kernel rejects a forwarded route lookup on an interface with
		// forwarding disabled. Install its guard before probing the route.
		if !tableExists {
			if _, err = r.command(ruleset(subnet, "", false, false), "nft", "-f", "-"); err != nil {
				return err
			}
			tableExists = true
		}
		if err = r.ensureForward(&a, "usb0"); err != nil {
			return err
		}
		uplink, routing = r.uplink(subnet)
		if uplink == "" {
			s.State = "waiting-uplink"
		} else {
			s.State = "active"
			s.Uplink = uplink
			policy, e := r.command("", "ip", "-j", "-4", "rule", "show")
			flow = e == nil && !foreign && defaultPolicy(policy)
			if !flow {
				s.OffloadReason = "network-policy"
			}
			if flow && a.FlowUnsupported && a.Routing == routing {
				flow = false
				s.OffloadReason = "unsupported"
			}
		}
	}
	// Cache only the canonical table text, not the optional delete prefix.
	canonical := ruleset(subnet, uplink, flow, false)
	if canonical != a.Rules || routing != a.Routing || !tableExists {
		if _, err = r.command(ruleset(subnet, uplink, flow, tableExists), "nft", "-f", "-"); err != nil {
			if !flow {
				return err
			}
			// Old kernels/uplinks may lack flowtable support. NAT must still work.
			flow = false
			a.FlowUnsupported = true
			s.OffloadReason = "unsupported"
			canonical = ruleset(subnet, uplink, false, false)
			if _, err = r.command(ruleset(subnet, uplink, false, tableExists), "nft", "-f", "-"); err != nil {
				return err
			}
		}
	}
	if flow {
		a.FlowUnsupported = false
	}
	a.Rules = canonical
	a.Routing = routing
	a.Uplink = uplink
	if err = r.forwarding(&a, uplink, foreign); err != nil {
		return err
	}
	config := dnsConfig(r.Resolv, prefix, uplink != "")
	if err = r.daemon(config); err != nil {
		return err
	}
	a.Config = config
	s.FlowOffload = flow
	return r.save("applied.json", a)
}
