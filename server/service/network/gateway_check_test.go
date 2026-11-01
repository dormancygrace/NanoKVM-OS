package network

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// checkWorld is the device the gateway check sees: the default routes in a
// model of the kernel's table, carrier per interface, the probes' answers
// and the DHCP restarts it asks for.
type checkWorld struct {
	table    kernelRoutes
	carrier  map[string]bool
	answers  map[string]bool // by interface
	host     map[string]bool // by interface, for the custom host
	restarts []int
	server   bool // a DHCP server answers a discover
	probes   int  // discovers sent
	clock    time.Time
}

func newCheckWorld(t *testing.T) *checkWorld {
	t.Helper()
	w := &checkWorld{
		table: kernelRoutes{
			202: {Interface: "eth0", Gateway: "192.168.10.1", Metric: 202},
			303: {Interface: "wlan0", Gateway: "192.168.10.1", Metric: 303},
		},
		carrier: map[string]bool{"eth0": true, "wlan0": true},
		answers: map[string]bool{"eth0": true, "wlan0": true},
		host:    map[string]bool{"eth0": true, "wlan0": true},
		server:  true,
		clock:   time.Unix(1000, 0),
	}
	useModel(t, w.table, w.table.run)
	dir := t.TempDir()
	saved := []any{gatewayRouteTable, gatewayDemotedDir, gatewaySysNet, gatewayProcDir, gatewayDHCPPidFile,
		probeGateway, probeHost, restartGatewayDHCP, gatewayWatch, probeDHCPServer}
	t.Cleanup(func() {
		gatewayRouteTable, gatewayDemotedDir = saved[0].(string), saved[1].(string)
		gatewaySysNet, gatewayProcDir, gatewayDHCPPidFile = saved[2].(string), saved[3].(string), saved[4].(string)
		probeGateway = saved[5].(func(string, string, string) bool)
		probeHost = saved[6].(func(string, string) bool)
		restartGatewayDHCP = saved[7].(func(int) error)
		gatewayWatch = saved[8].(*gatewayChecker)
		probeDHCPServer = saved[9].(func(string) bool)
	})
	probeDHCPServer = func(iface string) bool { w.probes++; return w.server }
	gatewayRouteTable = filepath.Join(dir, "route")
	gatewayDemotedDir = filepath.Join(dir, "demoted")
	gatewaySysNet = filepath.Join(dir, "net")
	gatewayProcDir = filepath.Join(dir, "proc")
	gatewayDHCPPidFile = filepath.Join(dir, "udhcpc.eth0.pid")
	probeGateway = func(iface, gateway, method string) bool { return w.answers[iface] }
	probeHost = func(iface, host string) bool { return w.host[iface] }
	restartGatewayDHCP = func(pid int) error { w.restarts = append(w.restarts, pid); return nil }
	gatewayWatch = &gatewayChecker{health: map[string]*gatewayHealth{}, now: func() time.Time { return w.clock }}
	return w
}

// sync writes the table and the carrier files the check reads.
func (w *checkWorld) sync(t *testing.T) {
	t.Helper()
	table := routeTableHeader
	for _, route := range w.table.entries() {
		var a, b, c, d int
		_, _ = fmtSscanf(route.Gateway, &a, &b, &c, &d)
		hex := strconv.FormatUint(uint64(d<<24|c<<16|b<<8|a), 16)
		for len(hex) < 8 {
			hex = "0" + hex
		}
		table += route.Interface + "\t00000000\t" + hex + "\t0003\t0\t0\t" + strconv.Itoa(route.Metric) + "\t00000000\t0\t0\t0\n"
	}
	if err := os.WriteFile(gatewayRouteTable, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	for iface, up := range w.carrier {
		dir := filepath.Join(gatewaySysNet, iface)
		_ = os.MkdirAll(dir, 0o755)
		value := "0\n"
		if up {
			value = "1\n"
		}
		_ = os.WriteFile(filepath.Join(dir, "carrier"), []byte(value), 0o644)
	}
}

func (w *checkWorld) tick(t *testing.T, config gatewayCheckConfig, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		w.sync(t)
		gatewayWatch.tick(config)
		w.clock = w.clock.Add(gatewayCheckInterval)
	}
	w.sync(t)
}

func (w *checkWorld) metricOf(iface string) int {
	for metric, route := range w.table {
		if route.Interface == iface {
			return metric
		}
	}
	return -1
}

func fmtSscanf(address string, a, b, c, d *int) (int, error) {
	parts := [4]*int{a, b, c, d}
	field := 0
	for _, ch := range address {
		if ch == '.' {
			field++
			continue
		}
		*parts[field] = *parts[field]*10 + int(ch-'0')
	}
	return 4, nil
}

var arpCheck = gatewayCheckConfig{Enabled: true, Method: "arp"}

func TestADeadEthernetGatewayMovesTrafficToWifiAndBack(t *testing.T) {
	w := newCheckWorld(t)
	w.answers["eth0"] = false
	w.tick(t, arpCheck, gatewayDemoteAfter-1)
	if w.metricOf("eth0") != 202 {
		t.Fatalf("demoted before %d failures: %+v", gatewayDemoteAfter, w.table)
	}
	w.tick(t, arpCheck, 1)
	if m := w.metricOf("eth0"); !isDemotedMetric(m) || w.metricOf("wlan0") != 303 || len(w.table) != 2 {
		t.Fatalf("not demoted: %+v", w.table)
	}
	if original, ok := demotedOriginal("eth0"); !ok || original != 202 {
		t.Fatalf("recorded metric %d %v", original, ok)
	}
	w.answers["eth0"] = true
	w.tick(t, arpCheck, gatewayRestoreAfter)
	if w.metricOf("eth0") != 202 || len(w.table) != 2 {
		t.Fatalf("not restored: %+v", w.table)
	}
	if _, ok := demotedOriginal("eth0"); ok {
		t.Fatal("demotion record left")
	}
}

// The ticks from the gateway's first silence to the DHCP restart.
var dhcpTicks = int(gatewayDHCPAfter / gatewayCheckInterval)

func TestASingleRouteIsNeverDemoted(t *testing.T) {
	w := newCheckWorld(t)
	delete(w.table, 303)
	w.answers["eth0"] = false
	w.tick(t, arpCheck, gatewayDemoteAfter*3)
	if w.metricOf("eth0") != 202 {
		t.Fatalf("%+v", w.table)
	}
}

func TestASilentEthernetGatewayRestartsDHCPOncePerHoldoff(t *testing.T) {
	w := newCheckWorld(t)
	delete(w.table, 303)
	_ = os.WriteFile(gatewayDHCPPidFile, []byte("4321\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(gatewayProcDir, "4321"), 0o755)
	_ = os.WriteFile(filepath.Join(gatewayProcDir, "4321", "comm"), []byte("udhcpc\n"), 0o644)
	w.answers["eth0"] = false
	w.tick(t, arpCheck, dhcpTicks)
	if len(w.restarts) != 0 {
		t.Fatalf("restarted early: %v", w.restarts)
	}
	w.tick(t, arpCheck, 1)
	if len(w.restarts) != 1 || w.restarts[0] != 4321 {
		t.Fatalf("restarts %v", w.restarts)
	}
	w.tick(t, arpCheck, dhcpTicks+1)
	if len(w.restarts) != 2 {
		t.Fatalf("after the holdoff: %v", w.restarts)
	}
}

func TestARouterThatIsDownKeepsTheLease(t *testing.T) {
	// No DHCP server answers: releasing the lease would leave Ethernet
	// without an address, so it stays, and the discover repeats after the
	// hold-off; once a server answers, DHCP restarts.
	w := newCheckWorld(t)
	delete(w.table, 303)
	_ = os.WriteFile(gatewayDHCPPidFile, []byte("4321\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(gatewayProcDir, "4321"), 0o755)
	_ = os.WriteFile(filepath.Join(gatewayProcDir, "4321", "comm"), []byte("udhcpc\n"), 0o644)
	w.answers["eth0"], w.server = false, false
	w.tick(t, arpCheck, dhcpTicks+1)
	if len(w.restarts) != 0 || w.probes != 1 {
		t.Fatalf("restarts %v, discovers %d", w.restarts, w.probes)
	}
	w.tick(t, arpCheck, int(gatewayCheckDHCPHoldoff/gatewayCheckInterval)-1)
	if w.probes != 1 {
		t.Fatalf("discovered again within the hold-off: %d", w.probes)
	}
	w.server = true
	w.tick(t, arpCheck, 1)
	if len(w.restarts) != 1 || w.probes != 2 {
		t.Fatalf("restarts %v, discovers %d", w.restarts, w.probes)
	}
}

func TestAPidFileOfAnotherProcessIsNotSignalled(t *testing.T) {
	w := newCheckWorld(t)
	delete(w.table, 303)
	_ = os.WriteFile(gatewayDHCPPidFile, []byte("4321\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(gatewayProcDir, "4321"), 0o755)
	_ = os.WriteFile(filepath.Join(gatewayProcDir, "4321", "comm"), []byte("sshd\n"), 0o644)
	w.answers["eth0"] = false
	w.tick(t, arpCheck, dhcpTicks*2)
	if len(w.restarts) != 0 {
		t.Fatalf("restarts %v", w.restarts)
	}
}

func TestTurningTheCheckOffRestoresTheRoutes(t *testing.T) {
	w := newCheckWorld(t)
	w.answers["eth0"] = false
	w.tick(t, arpCheck, gatewayDemoteAfter)
	if !isDemotedMetric(w.metricOf("eth0")) {
		t.Fatalf("%+v", w.table)
	}
	w.tick(t, gatewayCheckConfig{Method: "arp"}, 1)
	if w.metricOf("eth0") != 202 {
		t.Fatalf("not restored: %+v", w.table)
	}
}

func TestGatewayCheckValidation(t *testing.T) {
	for _, config := range []gatewayCheckConfig{
		{Method: "arp"}, {Method: "icmp", Host: "1.1.1.1"}, {Method: "arp", Host: "example.com"}, {Method: "icmp", Host: "2606:4700::1111"},
	} {
		if !validGatewayCheck(config) {
			t.Errorf("rejected %+v", config)
		}
	}
	for _, config := range []gatewayCheckConfig{
		{Method: "udp"}, {Method: "arp", Host: "-c 100"}, {Method: "arp", Host: "a b"}, {Method: "arp", Host: "x;reboot"},
	} {
		if validGatewayCheck(config) {
			t.Errorf("accepted %+v", config)
		}
	}
}
