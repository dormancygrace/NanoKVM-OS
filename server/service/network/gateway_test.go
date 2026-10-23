package network

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"NanoKVM-Server/proto"
)

const routeTableHeader = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"

func TestParseDefaultGatewayRoutesOnlyReturnsPhysicalInterfaces(t *testing.T) {
	routes := parseDefaultGatewayRoutes(routeTableHeader +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"wlan0\t00000000\t0100000A\t0003\t0\t0\t200\t00000000\t0\t0\t0\n" +
		"tun0\t00000000\t00000000\t0001\t0\t0\t50\t00000000\t0\t0\t0\n" +
		"wg0\t00000000\t0100070A\t0003\t0\t0\t10\t00000000\t0\t0\t0\n" +
		"*\t00000000\t00000000\t0201\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n")
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2: %+v", len(routes), routes)
	}
	if routes[0].Interface != "eth0" || routes[0].Gateway != "192.168.1.1" || routes[0].Metric != 100 {
		t.Fatalf("unexpected Ethernet route: %+v", routes[0])
	}
	if routes[1].Interface != "wlan0" || routes[1].Gateway != "10.0.0.1" || routes[1].Metric != 200 {
		t.Fatalf("unexpected Wi-Fi route: %+v", routes[1])
	}
}

func TestDefaultGatewayRoutesReadsTheRouteTable(t *testing.T) {
	previous := gatewayRouteTable
	defer func() { gatewayRouteTable = previous }()
	gatewayRouteTable = filepath.Join(t.TempDir(), "route")
	if routes := defaultGatewayRoutes(); routes == nil || len(routes) != 0 {
		t.Fatalf("unreadable table: %+v", routes)
	}
	_ = os.WriteFile(gatewayRouteTable, []byte(routeTableHeader+"wlan0\t00000000\t0104A8C0\t0003\t0\t0\t303\t00000000\t0\t0\t0\n"), 0600)
	routes := defaultGatewayRoutes()
	if len(routes) != 1 || routes[0].Interface != "wlan0" || routes[0].Gateway != "192.168.4.1" || routes[0].Metric != 303 {
		t.Fatalf("%+v", routes)
	}
}

// kernelRoutes models the main table's default routes: the kernel keys them
// by metric, so a replace at a metric another gateway holds replaces it.
type kernelRoutes map[int]proto.GatewayRoute

func (k kernelRoutes) run(args ...string) ([]byte, error) {
	// -4 route replace|del default via GW dev IF [proto dhcp] metric M
	gateway, iface, metric := args[5], args[7], 0
	metric, _ = strconv.Atoi(args[len(args)-1])
	switch args[2] {
	case "replace":
		k[metric] = proto.GatewayRoute{Interface: iface, Gateway: gateway, Metric: metric}
	case "del":
		route, ok := k[metric]
		if !ok || route.Gateway != gateway || route.Interface != iface {
			return nil, fmt.Errorf("RTNETLINK answers: No such process")
		}
		delete(k, metric)
	}
	return nil, nil
}

func (k kernelRoutes) entries() []proto.GatewayRoute {
	var metrics []int
	for metric := range k {
		metrics = append(metrics, metric)
	}
	sort.Ints(metrics)
	var entries []proto.GatewayRoute
	for _, metric := range metrics {
		entries = append(entries, k[metric])
	}
	return entries
}

func TestPreferenceChangesKeepOneRoutePerGatewayThroughout(t *testing.T) {
	previous := gatewayRouteCommand
	defer func() { gatewayRouteCommand = previous }()
	table := kernelRoutes{
		202: {Interface: "eth0", Gateway: "192.168.1.1", Metric: 202},
		303: {Interface: "wlan0", Gateway: "192.168.4.1", Metric: 303},
	}
	gateways := map[string]bool{"192.168.1.1": true, "192.168.4.1": true}
	gatewayRouteCommand = func(args ...string) ([]byte, error) {
		out, err := table.run(args...)
		// After every step each gateway still has a route.
		have := map[string]bool{}
		for _, route := range table {
			have[route.Gateway] = true
		}
		for gateway := range gateways {
			if !have[gateway] {
				t.Fatalf("no route through %s after %v: %+v", gateway, args, table)
			}
		}
		return out, err
	}
	for _, preferred := range []string{"ethernet", "wifi", "ethernet", "auto", "wifi", "wifi", "ethernet"} {
		if err := applyGatewayPreference(preferred, table.entries()); err != nil {
			t.Fatalf("%s: %v", preferred, err)
		}
		if len(table) != 2 {
			t.Fatalf("%s: routes %+v", preferred, table)
		}
		for _, route := range table {
			if route.Metric != preferenceMetric(preferred, route.Interface, map[string]int{"eth0": 0, "wlan0": 1}[route.Interface]) {
				t.Fatalf("%s: %+v", preferred, table)
			}
		}
	}
}

func TestAGatewayWithSeveralMetricsEndsWithOne(t *testing.T) {
	previous := gatewayRouteCommand
	defer func() { gatewayRouteCommand = previous }()
	table := kernelRoutes{
		10:  {Interface: "wlan0", Gateway: "192.168.4.1", Metric: 10},
		202: {Interface: "eth0", Gateway: "192.168.1.1", Metric: 202},
		303: {Interface: "wlan0", Gateway: "192.168.4.1", Metric: 303},
	}
	gatewayRouteCommand = table.run
	if err := applyGatewayPreference("ethernet", table.entries()); err != nil {
		t.Fatal(err)
	}
	want := kernelRoutes{
		10:  {Interface: "eth0", Gateway: "192.168.1.1", Metric: 10},
		201: {Interface: "wlan0", Gateway: "192.168.4.1", Metric: 201},
	}
	if !reflect.DeepEqual(table, want) {
		t.Fatalf("%+v", table)
	}
}

func TestAFailedReplaceKeepsTheOldRoute(t *testing.T) {
	previous := gatewayRouteCommand
	defer func() { gatewayRouteCommand = previous }()
	table := kernelRoutes{202: {Interface: "eth0", Gateway: "192.168.1.1", Metric: 202}}
	gatewayRouteCommand = func(args ...string) ([]byte, error) {
		if args[2] == "replace" {
			return nil, fmt.Errorf("RTNETLINK answers: Network is unreachable")
		}
		return table.run(args...)
	}
	if err := applyGatewayPreference("ethernet", table.entries()); err == nil {
		t.Fatal("a failed replace was reported as applied")
	}
	if _, ok := table[202]; !ok || len(table) != 1 {
		t.Fatalf("the old route was removed: %+v", table)
	}
}

// Against the real kernel, in a network namespace of its own:
// NANOKVM_NETNS_TEST=1 unshare -rn go test -run InANetworkNamespace
func TestPreferenceChangesInANetworkNamespace(t *testing.T) {
	if os.Getenv("NANOKVM_NETNS_TEST") != "1" {
		t.Skip("needs NANOKVM_NETNS_TEST=1 inside a network namespace")
	}
	ip := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %v: %v %s", args, err, out)
		}
	}
	ip("link", "add", "eth0", "type", "dummy")
	ip("link", "add", "wlan0", "type", "dummy")
	ip("link", "set", "eth0", "up")
	ip("link", "set", "wlan0", "up")
	ip("addr", "add", "192.168.1.20/24", "dev", "eth0")
	ip("addr", "add", "192.168.4.128/24", "dev", "wlan0")
	ip("route", "add", "default", "via", "192.168.1.1", "dev", "eth0", "metric", "202")
	ip("route", "add", "default", "via", "192.168.4.1", "dev", "wlan0", "metric", "303")
	for _, preferred := range []string{"ethernet", "wifi", "ethernet", "auto", "wifi", "wifi", "ethernet", "auto"} {
		if err := applyGatewayPreference(preferred, defaultGatewayEntries()); err != nil {
			t.Fatalf("%s: %v", preferred, err)
		}
		entries := defaultGatewayEntries()
		if len(entries) != 2 {
			t.Fatalf("%s: %+v", preferred, entries)
		}
		for _, route := range entries {
			want := preferenceMetric(preferred, route.Interface, map[string]int{"eth0": 0, "wlan0": 1}[route.Interface])
			if route.Metric != want {
				t.Fatalf("%s: %+v", preferred, entries)
			}
		}
	}
}
