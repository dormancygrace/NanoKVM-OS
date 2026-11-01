package network

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// The optional Ethernet gateway check. Every few seconds the Ethernet
// default gateway is probed through Ethernet (ARP by default: a router may
// drop ICMP, not ARP), and optionally a host behind it with ping. While the
// check fails and Wi-Fi is connected, the Ethernet default route moves to
// the demoted metrics, below Wi-Fi's, until the check passes again: the
// device then reaches the Internet, and remote access reaches it, over
// Wi-Fi. A gateway silent for two minutes, as one handed out by a DHCP
// server that went away, has udhcpc release and discover again, when a
// DHCP server answers at all (sipeed/NanoKVM#964).
//
// A link without carrier is the kernel's business (ignore_routes_with_linkdown
// in sysctl.d/40-nanokvm-multihome.conf): Ethernet without carrier is not
// probed, and Wi-Fi is not probed at all, as it loses its link when the
// access point goes. Only IPv4 default routes are checked.
var (
	gatewayCheckFile        = "/etc/kvm/network/gateway.check"
	gatewayDemotedDir       = "/run/nanokvm/gateway-demoted"
	gatewayCheckInterval    = 5 * time.Second
	gatewayCheckDHCPHoldoff = 60 * time.Second
	gatewaySysNet           = "/sys/class/net"
	gatewayProcDir          = "/proc"
)

const (
	// Demoted routes take 900 and up: below the preference metrics (10, 100,
	// 200 and up) and udhcpc's own (200 + interface index), above the interim
	// ones applyGatewayPreference moves routes through (1000 and up). The
	// udhcpc hook (scripts/99-nanokvm-gateway) uses the same band.
	gatewayDemotedMetric = 900
	gatewayDemoteAfter   = 3               // failed checks in a row
	gatewayRestoreAfter  = 2               // passed checks in a row
	gatewayDHCPAfter     = 2 * time.Minute // of an unanswered gateway
)

type gatewayCheckConfig struct {
	Enabled bool   `json:"enabled"`
	Method  string `json:"method"`
	Host    string `json:"host"`
}

var gatewayHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$`)

func validGatewayCheck(config gatewayCheckConfig) bool {
	if config.Method != "arp" && config.Method != "icmp" {
		return false
	}
	return config.Host == "" || gatewayHostPattern.MatchString(config.Host)
}

func readGatewayCheck() gatewayCheckConfig {
	config := gatewayCheckConfig{Method: "arp"}
	contents, err := os.ReadFile(gatewayCheckFile)
	if err != nil {
		return config
	}
	var stored gatewayCheckConfig
	if json.Unmarshal(contents, &stored) != nil || !validGatewayCheck(stored) {
		return config
	}
	return stored
}

func writeGatewayCheck(config gatewayCheckConfig) error {
	contents, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(gatewayCheckFile), 0o755); err != nil {
		return fmt.Errorf("create gateway configuration: %w", err)
	}
	temporary := gatewayCheckFile + ".tmp"
	if err := os.WriteFile(temporary, append(contents, '\n'), 0o600); err != nil {
		return fmt.Errorf("write gateway check: %w", err)
	}
	return os.Rename(temporary, gatewayCheckFile)
}

// Probes, replaced in tests. Each waits up to 2 s for one answer: Wi-Fi
// power save alone can delay one past a second.
var probeGateway = func(iface, gateway, method string) bool {
	if method == "icmp" {
		return exec.Command("ping", "-q", "-c", "1", "-W", "2", "-I", iface, gateway).Run() == nil
	}
	return exec.Command("arping", "-q", "-c", "1", "-w", "2", "-I", iface, gateway).Run() == nil
}

var probeHost = func(iface, host string) bool {
	return exec.Command("ping", "-q", "-c", "1", "-W", "2", "-I", iface, host).Run() == nil
}

func gatewayCarrier(iface string) bool {
	contents, err := os.ReadFile(filepath.Join(gatewaySysNet, iface, "carrier"))
	return err == nil && strings.TrimSpace(string(contents)) == "1"
}

// The udhcpc serving an Ethernet interface, from the pid file S30eth gives
// it (eth0's also for a VLAN), and only while that process is udhcpc. None
// on a static address. Wi-Fi asks for a lease on every association, and its
// udhcpc belongs to S30wifi.
var gatewayDHCPPidFile = "/run/udhcpc.eth0.pid"

func gatewayDHCPClient(iface string) int {
	if !strings.HasPrefix(iface, "eth") {
		return 0
	}
	contents, err := os.ReadFile(gatewayDHCPPidFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || pid <= 1 {
		return 0
	}
	comm, err := os.ReadFile(filepath.Join(gatewayProcDir, strconv.Itoa(pid), "comm"))
	if err != nil || strings.TrimSpace(string(comm)) != "udhcpc" {
		return 0
	}
	return pid
}

// Whether a DHCP server answers on the interface: a one-shot discover whose
// lease is not applied (-s /bin/true). A router that is down, rather than a
// stale lease, answers nothing; releasing the lease then would leave the
// interface without an address, unreachable even from the local network
// (sipeed/NanoKVM#964).
var probeDHCPServer = func(iface string) bool {
	return exec.Command("udhcpc", "-i", iface, "-n", "-q", "-f", "-s", "/bin/true", "-t", "3", "-T", "2", "-A", "0").Run() == nil
}

// Release, then discover again: udhcpc only looks for another server when
// its lease runs out.
var restartGatewayDHCP = func(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
		return err
	}
	return syscall.Kill(pid, syscall.SIGUSR1)
}

// gatewayMu orders route changes of the check and of the preference.
var gatewayMu sync.Mutex

// demotedOriginal is the metric an interface's route had before it was
// demoted, which the route gets back.
func demotedOriginal(iface string) (int, bool) {
	contents, err := os.ReadFile(filepath.Join(gatewayDemotedDir, iface))
	if err != nil {
		return 0, false
	}
	metric, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	return metric, err == nil
}

func writeDemotedOriginal(iface string, metric int) error {
	if err := os.MkdirAll(gatewayDemotedDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(gatewayDemotedDir, iface), []byte(strconv.Itoa(metric)+"\n"), 0o644)
}

func isDemotedMetric(metric int) bool {
	return metric >= gatewayDemotedMetric && metric < 1000
}

// moveGatewayRoute gives a route the first free metric from `from`, adding
// the new route before deleting the old ones, so that the gateway keeps a
// route throughout.
func moveGatewayRoute(route proto.GatewayRoute, entries []proto.GatewayRoute, from int, limit int) (int, error) {
	used := occupiedMetrics()
	for _, entry := range entries {
		used[entry.Metric] = true
	}
	var err error
	for metric := from; metric < limit; metric++ {
		if used[metric] {
			continue
		}
		if err = addGatewayRoute(route, metric); err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Interface == route.Interface && entry.Gateway == route.Gateway && entry.Metric != metric {
				if err := deleteGatewayRoute(route, entry.Metric); err != nil {
					return metric, err
				}
			}
		}
		return metric, nil
	}
	if err == nil {
		err = fmt.Errorf("no free metric")
	}
	return 0, err
}

func demoteGateway(route proto.GatewayRoute, entries []proto.GatewayRoute) error {
	if err := writeDemotedOriginal(route.Interface, route.Metric); err != nil {
		return err
	}
	_, err := moveGatewayRoute(route, entries, gatewayDemotedMetric, 1000)
	return err
}

func restoreGateway(route proto.GatewayRoute, entries []proto.GatewayRoute) error {
	original, ok := demotedOriginal(route.Interface)
	if !ok || isDemotedMetric(original) {
		original = preferenceMetric(readGatewayPreference(), route.Interface, 0)
	}
	if _, err := moveGatewayRoute(route, entries, original, gatewayDemotedMetric); err != nil {
		return err
	}
	return os.Remove(filepath.Join(gatewayDemotedDir, route.Interface))
}

// restoreAllGateways undoes every demotion: the check is off.
func restoreAllGateways() {
	files, err := os.ReadDir(gatewayDemotedDir)
	if err != nil {
		return
	}
	entries := defaultGatewayEntries()
	for _, file := range files {
		restored := false
		for _, route := range uniqueGatewayRoutes(entries) {
			if route.Interface == file.Name() {
				if err := restoreGateway(route, entries); err != nil {
					log.Warnf("gateway check: restore %s: %v", route.Interface, err)
				}
				restored = true
			}
		}
		if !restored {
			_ = os.Remove(filepath.Join(gatewayDemotedDir, file.Name()))
		}
	}
}

type gatewayHealth struct {
	passes, fails      int
	silentSince        time.Time // the gateway's first unanswered probe; zero while it answers
	lastDHCP           time.Time
	checked, reachable bool
	keptLease          bool // logged once while no DHCP server answers
}

type gatewayChecker struct {
	mu     sync.Mutex
	health map[string]*gatewayHealth // by interface
	now    func() time.Time
}

var gatewayWatch = &gatewayChecker{health: map[string]*gatewayHealth{}, now: time.Now}

// tick runs one round of checks and the route changes they call for.
func (c *gatewayChecker) tick(config gatewayCheckConfig) {
	if !config.Enabled {
		c.mu.Lock()
		c.health = map[string]*gatewayHealth{}
		c.mu.Unlock()
		gatewayMu.Lock()
		restoreAllGateways()
		gatewayMu.Unlock()
		return
	}
	// The probes take seconds while a gateway is silent: outside gatewayMu,
	// so that the preferred-gateway setting does not wait for them.
	now := c.now()
	results := map[string]*gatewayHealth{}
	for _, route := range uniqueGatewayRoutes(defaultGatewayEntries()) {
		if _, seen := results[route.Interface]; seen || gatewayKind(route.Interface) != "ethernet" || !gatewayCarrier(route.Interface) {
			continue
		}
		c.mu.Lock()
		health := c.health[route.Interface]
		if health == nil {
			health = &gatewayHealth{}
		}
		c.mu.Unlock()
		gatewayOK := probeGateway(route.Interface, route.Gateway, config.Method)
		ok := gatewayOK && (config.Host == "" || probeHost(route.Interface, config.Host))
		if ok {
			health.passes, health.fails = health.passes+1, 0
		} else {
			health.passes, health.fails = 0, health.fails+1
		}
		if gatewayOK {
			health.silentSince = time.Time{}
		} else if health.silentSince.IsZero() {
			health.silentSince = now
		}
		health.checked, health.reachable = true, ok
		results[route.Interface] = health
	}
	c.mu.Lock()
	c.health = results
	c.mu.Unlock()

	// The table again: routes may have moved while the probes ran.
	gatewayMu.Lock()
	entries := defaultGatewayEntries()
	routes := uniqueGatewayRoutes(entries)
	var silent []proto.GatewayRoute
	for _, route := range routes {
		health := results[route.Interface]
		// A route in the demoted band without its record (a renew moved it
		// there between two steps of a restore) is demoted all the same.
		_, recorded := demotedOriginal(route.Interface)
		demoted := recorded || isDemotedMetric(route.Metric)
		switch {
		case health == nil:
			// Without carrier the kernel ignores the route; a demotion stays
			// for when the link comes back, and the check decides then.
		case demoted && health.passes >= gatewayRestoreAfter:
			if err := restoreGateway(route, entries); err != nil {
				log.Warnf("gateway check: restore %s: %v", route.Interface, err)
			} else {
				log.Infof("gateway check: %s via %s answers again, route restored", route.Interface, route.Gateway)
			}
		case !demoted && health.fails >= gatewayDemoteAfter && wifiConnected(routes):
			if err := demoteGateway(route, entries); err != nil {
				log.Warnf("gateway check: demote %s: %v", route.Interface, err)
			} else {
				log.Warnf("gateway check: %s via %s does not answer, traffic moves to the other connection", route.Interface, route.Gateway)
			}
		}
		if health != nil && !health.silentSince.IsZero() && now.Sub(health.silentSince) >= gatewayDHCPAfter &&
			now.Sub(health.lastDHCP) >= gatewayCheckDHCPHoldoff {
			silent = append(silent, route)
		}
	}
	// A demotion whose interface lost its default route is over.
	if files, err := os.ReadDir(gatewayDemotedDir); err == nil {
		for _, file := range files {
			found := false
			for _, route := range routes {
				found = found || route.Interface == file.Name()
			}
			if !found {
				_ = os.Remove(filepath.Join(gatewayDemotedDir, file.Name()))
			}
		}
	}
	gatewayMu.Unlock()

	// The discover takes seconds too: outside gatewayMu as well.
	for _, route := range silent {
		health := results[route.Interface]
		pid := gatewayDHCPClient(route.Interface)
		if pid == 0 {
			continue
		}
		health.lastDHCP = now
		if !probeDHCPServer(route.Interface) {
			if !health.keptLease {
				log.Warnf("gateway check: gateway %s silent and no DHCP server answers on %s, keeping the lease", route.Gateway, route.Interface)
			}
			health.keptLease = true
			continue
		}
		health.silentSince, health.keptLease = time.Time{}, false
		if err := restartGatewayDHCP(pid); err != nil {
			log.Warnf("gateway check: restart DHCP on %s: %v", route.Interface, err)
		} else {
			log.Warnf("gateway check: gateway %s silent for 2 minutes, DHCP on %s starts again", route.Gateway, route.Interface)
		}
	}
}

// wifiConnected: a Wi-Fi default route whose interface has its link, for
// the traffic to move to. Otherwise nothing is demoted.
func wifiConnected(routes []proto.GatewayRoute) bool {
	for _, route := range routes {
		if gatewayKind(route.Interface) == "wifi" && gatewayCarrier(route.Interface) {
			return true
		}
	}
	return false
}

func (c *gatewayChecker) status(iface string) (checked, reachable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if health := c.health[iface]; health != nil {
		return health.checked, health.reachable
	}
	return false, false
}

var startGatewayCheckOnce sync.Once

// StartGatewayCheck runs the check for the server's lifetime; it does
// nothing until it is enabled.
func StartGatewayCheck() {
	startGatewayCheckOnce.Do(func() {
		go func() {
			for {
				gatewayWatch.tick(readGatewayCheck())
				time.Sleep(gatewayCheckInterval)
			}
		}()
	})
}

func gatewayStatusRoutes() []proto.GatewayRoute {
	routes := defaultGatewayRoutes()
	for index := range routes {
		_, routes[index].Demoted = demotedOriginal(routes[index].Interface)
		if checked, reachable := gatewayWatch.status(routes[index].Interface); checked {
			routes[index].Reachable = &reachable
		}
	}
	return routes
}

func (s *Service) GetGatewayCheck(c *gin.Context) {
	var rsp proto.Response
	config := readGatewayCheck()
	state := proto.GatewayCheckRsp{Enabled: config.Enabled, Method: config.Method, Host: config.Host, Wifi: isSupported()}
	for _, route := range gatewayStatusRoutes() {
		if gatewayKind(route.Interface) == "ethernet" {
			state.Gateway, state.Reachable, state.Demoted = route.Gateway, route.Reachable, route.Demoted
			state.Carrier = gatewayCarrier(route.Interface)
			break
		}
	}
	rsp.OkRspWithData(c, &state)
}

func (s *Service) SetGatewayCheck(c *gin.Context) {
	var req proto.SetGatewayCheckReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid gateway check")
		return
	}
	config := gatewayCheckConfig{Enabled: req.Enabled, Method: req.Method, Host: strings.TrimSpace(req.Host)}
	if config.Method == "" {
		config.Method = "arp"
	}
	if !validGatewayCheck(config) {
		rsp.ErrRsp(c, -1, "invalid gateway check")
		return
	}
	if err := writeGatewayCheck(config); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	if !config.Enabled {
		gatewayWatch.tick(config)
	}
	rsp.OkRsp(c)
}
