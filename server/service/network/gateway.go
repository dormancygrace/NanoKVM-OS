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
const udhcpcGatewayHookFile = "/usr/share/udhcpc/default.script.d/99-nanokvm-gateway"

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
	if err := applyGatewayPreference(req.Preferred, defaultGatewayRoutes()); err != nil {
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	rsp.OkRsp(c)
}

func installGatewayHook() error {
	if err := os.MkdirAll(filepath.Dir(udhcpcGatewayHookFile), 0o755); err != nil {
		return fmt.Errorf("create DHCP hook directory: %w", err)
	}
	if err := os.WriteFile(udhcpcGatewayHookFile, []byte(udhcpcGatewayHook), 0o755); err != nil {
		return fmt.Errorf("install gateway DHCP hook: %w", err)
	}
	return nil
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

func defaultGatewayRoutes() []proto.GatewayRoute {
	output, err := gatewayRouteCommand("-4", "route", "show", "default")
	if err != nil {
		return []proto.GatewayRoute{}
	}
	return parseDefaultGatewayRoutes(string(output))
}

func parseDefaultGatewayRoutes(output string) []proto.GatewayRoute {
	routes := []proto.GatewayRoute{}
	seen := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "default" {
			continue
		}
		var gateway, iface string
		metric := 0
		for i := 0; i+1 < len(fields); i++ {
			switch fields[i] {
			case "via":
				gateway = fields[i+1]
			case "dev":
				iface = fields[i+1]
			case "metric":
				metric, _ = strconv.Atoi(fields[i+1])
			}
		}
		if gateway == "" || !isGatewayInterface(iface) {
			continue
		}
		key := iface + "\x00" + gateway
		if !seen[key] {
			seen[key] = true
			routes = append(routes, proto.GatewayRoute{Interface: iface, Gateway: gateway, Metric: metric})
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

func applyGatewayPreference(preferred string, routes []proto.GatewayRoute) error {
	for index, route := range routes {
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
	}
	return nil
}
