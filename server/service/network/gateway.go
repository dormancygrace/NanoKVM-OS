package network

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// applyGatewayPreference gives each gateway the metric of the preference.
// The metric is part of a route's identity, so replace adds a route next to
// one with another metric: those are deleted after the new one is in.
func applyGatewayPreference(preferred string, entries []proto.GatewayRoute) error {
	for index, route := range uniqueGatewayRoutes(entries) {
		metric := 100 + index
		if preferred != "auto" {
			if gatewayKind(route.Interface) == preferred {
				metric = 10 + index
			} else {
				metric = 200 + index
			}
		}
		if _, err := gatewayRouteCommand("-4", "route", "replace", "default", "via", route.Gateway, "dev", route.Interface, "proto", "dhcp", "metric", strconv.Itoa(metric)); err != nil {
			return fmt.Errorf("apply route for %s: %w", route.Interface, err)
		}
		for _, old := range entries {
			if old.Interface != route.Interface || old.Gateway != route.Gateway || old.Metric == metric {
				continue
			}
			if _, err := gatewayRouteCommand("-4", "route", "del", "default", "via", old.Gateway, "dev", old.Interface, "metric", strconv.Itoa(old.Metric)); err != nil {
				return fmt.Errorf("remove the old route for %s: %w", route.Interface, err)
			}
		}
	}
	return nil
}
