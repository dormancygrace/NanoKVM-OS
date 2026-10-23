package network

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
)

const gatewayPreferenceFile = "/etc/kvm/network/gateway.preferred"
const udhcpcGatewayHookName = "99-nanokvm-gateway"

//go:embed scripts/99-nanokvm-gateway
var udhcpcGatewayHook string

var gatewayRouteCommand = func(args ...string) ([]byte, error) {
	return exec.Command("ip", args...).Output()
}

func (s *Service) GetGatewayPreference(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, &proto.GatewayPreferenceRsp{
		Preferred: readGatewayPreference(),
		Routes:    defaultGatewayRoutes(),
	})
}

func (s *Service) SetGatewayPreference(c *gin.Context) {
	var req proto.SetGatewayPreferenceReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid gateway preference")
		return
	}

	if err := installGatewayHook(); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	if err := writeGatewayPreference(req.Preferred); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	if err := applyGatewayPreference(req.Preferred, defaultGatewayEntries()); err != nil {
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	rsp.OkRsp(c)
}

func installGatewayHook() error {
	return installUDHCPCHook(udhcpcGatewayHookName, udhcpcGatewayHook)
}

func readGatewayPreference() string {
	contents, err := os.ReadFile(gatewayPreferenceFile)
	if err != nil {
		return "auto"
	}
	value := strings.TrimSpace(string(contents))
	if value == "ethernet" || value == "wifi" || value == "auto" {
		return value
	}
	return "auto"
}

func writeGatewayPreference(preferred string) error {
	if preferred != "auto" && preferred != "ethernet" && preferred != "wifi" {
		return fmt.Errorf("invalid gateway preference")
	}
	if err := os.MkdirAll(filepath.Dir(gatewayPreferenceFile), 0o755); err != nil {
		return fmt.Errorf("create gateway configuration: %w", err)
	}
	temporary := gatewayPreferenceFile + ".tmp"
	if err := os.WriteFile(temporary, []byte(preferred+"\n"), 0o600); err != nil {
		return fmt.Errorf("write gateway configuration: %w", err)
	}
	return os.Rename(temporary, gatewayPreferenceFile)
}

// The kernel's main IPv4 table, which ip -4 route show lists, without
// starting ip each time the network settings open.
var gatewayRouteTable = "/proc/net/route"

func defaultGatewayRoutes() []proto.GatewayRoute {
	return uniqueGatewayRoutes(defaultGatewayEntries())
}

func defaultGatewayEntries() []proto.GatewayRoute {
	table, err := os.ReadFile(gatewayRouteTable)
	if err != nil {
		return []proto.GatewayRoute{}
	}
	return parseDefaultGatewayEntries(string(table))
}

// parseDefaultGatewayRoutes lists each gateway of an interface once.
func parseDefaultGatewayRoutes(table string) []proto.GatewayRoute {
	return uniqueGatewayRoutes(parseDefaultGatewayEntries(table))
}

// parseDefaultGatewayEntries reads /proc/net/route: Iface, Destination,
// Gateway, Flags, RefCnt, Use, Metric, Mask, ... with addresses in hex. The
// same gateway can be there more than once, with different metrics.
func parseDefaultGatewayEntries(table string) []proto.GatewayRoute {
	routes := []proto.GatewayRoute{}
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		iface, gateway := fields[0], parseRouteGateway(fields[2])
		metric, _ := strconv.Atoi(fields[6])
		if gateway == "" || !isGatewayInterface(iface) {
			continue
		}
		routes = append(routes, proto.GatewayRoute{Interface: iface, Gateway: gateway, Metric: metric})
	}
	return routes
}

func uniqueGatewayRoutes(entries []proto.GatewayRoute) []proto.GatewayRoute {
	routes := []proto.GatewayRoute{}
	seen := map[string]bool{}
	for _, route := range entries {
		key := route.Interface + "\x00" + route.Gateway
		if !seen[key] {
			seen[key] = true
			routes = append(routes, route)
		}
	}
	return routes
}

func isGatewayInterface(iface string) bool {
	return strings.HasPrefix(iface, "eth") || strings.HasPrefix(iface, "wlan")
}

func gatewayKind(iface string) string {
	if strings.HasPrefix(iface, "eth") {
		return "ethernet"
	}
	if strings.HasPrefix(iface, "wlan") {
		return "wifi"
	}
	return ""
}

func preferenceMetric(preferred string, iface string, index int) int {
	if preferred == "auto" {
		return 100 + index
	}
	if gatewayKind(iface) == preferred {
		return 10 + index
	}
	return 200 + index
}

func setGatewayRoute(route proto.GatewayRoute, metric int) error {
	_, err := gatewayRouteCommand("-4", "route", "replace", "default", "via", route.Gateway, "dev", route.Interface, "proto", "dhcp", "metric", strconv.Itoa(metric))
	return err
}

// addGatewayRoute fails when any default route holds the metric already.
func addGatewayRoute(route proto.GatewayRoute, metric int) error {
	_, err := gatewayRouteCommand("-4", "route", "add", "default", "via", route.Gateway, "dev", route.Interface, "proto", "dhcp", "metric", strconv.Itoa(metric))
	return err
}

// The metrics of every default route in the main table, also those of
// interfaces this does not manage, such as a VPN or USB networking.
var occupiedMetrics = func() map[int]bool {
	metrics := map[int]bool{}
	table, err := os.ReadFile(gatewayRouteTable)
	if err != nil {
		return metrics
	}
	for _, line := range strings.Split(string(table), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		if metric, err := strconv.Atoi(fields[6]); err == nil {
			metrics[metric] = true
		}
	}
	return metrics
}

func deleteGatewayRoute(route proto.GatewayRoute, metric int) error {
	_, err := gatewayRouteCommand("-4", "route", "del", "default", "via", route.Gateway, "dev", route.Interface, "metric", strconv.Itoa(metric))
	return err
}

// applyGatewayPreference gives each gateway the metric of the preference,
// one route each. The kernel keys a default route by its metric, not by its
// gateway or device: a replace at a metric another gateway holds would
// replace that gateway's route. So each gateway first moves to a metric
// nobody uses, then to its preferred one, which is free by then; a route is
// deleted only after the one replacing it is in, so every gateway keeps a
// route throughout.
func applyGatewayPreference(preferred string, entries []proto.GatewayRoute) error {
	// A stable order: the table lists routes by metric, which this changes.
	routes := uniqueGatewayRoutes(entries)
	sort.Slice(routes, func(a, b int) bool {
		if routes[a].Interface != routes[b].Interface {
			return routes[a].Interface < routes[b].Interface
		}
		return routes[a].Gateway < routes[b].Gateway
	})
	// Routes of other interfaces keep their metrics: a preferred metric one
	// of them holds moves to the next free one.
	managed := map[int]bool{}
	for _, entry := range entries {
		managed[entry.Metric] = true
	}
	used := map[int]bool{}
	for metric := range occupiedMetrics() {
		used[metric] = true
	}
	for metric := range managed {
		used[metric] = true
	}
	desired := make([]int, len(routes))
	taken := map[int]bool{}
	for index, route := range routes {
		metric := preferenceMetric(preferred, route.Interface, index)
		for taken[metric] || (used[metric] && !managed[metric]) {
			metric++
		}
		desired[index] = metric
		taken[metric] = true
		used[metric] = true
	}
	metricsOf := func(route proto.GatewayRoute) []int {
		var metrics []int
		for _, entry := range entries {
			if entry.Interface == route.Interface && entry.Gateway == route.Gateway {
				metrics = append(metrics, entry.Metric)
			}
		}
		return metrics
	}
	current := make([]int, len(routes))
	next := 1000
	for index, route := range routes {
		old := metricsOf(route)
		if len(old) == 1 && old[0] == desired[index] {
			current[index] = desired[index]
			continue
		}
		// add, not replace: it fails rather than take a metric someone holds.
		var err error
		for attempt := 0; attempt < 16; attempt++ {
			for used[next] {
				next++
			}
			used[next] = true
			if err = addGatewayRoute(route, next); err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("apply route for %s: %w", route.Interface, err)
		}
		current[index] = next
		for _, metric := range old {
			if err := deleteGatewayRoute(route, metric); err != nil {
				return fmt.Errorf("remove the old route for %s: %w", route.Interface, err)
			}
		}
	}
	for index, route := range routes {
		if current[index] == desired[index] {
			continue
		}
		if err := setGatewayRoute(route, desired[index]); err != nil {
			return fmt.Errorf("apply route for %s: %w", route.Interface, err)
		}
		if err := deleteGatewayRoute(route, current[index]); err != nil {
			return fmt.Errorf("remove the interim route for %s: %w", route.Interface, err)
		}
	}
	return nil
}
