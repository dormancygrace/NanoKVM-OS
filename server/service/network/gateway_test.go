package network

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
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

// kernelRoutes models the main table's default routes: the kernel keys them
// by metric, so a replace at a metric another gateway holds replaces it.
type kernelRoutes map[int]proto.GatewayRoute

func (k kernelRoutes) run(args ...string) ([]byte, error) {
	// -4 route replace|del default via GW dev IF [proto dhcp] metric M
	gateway, iface, metric := args[5], args[7], 0
	metric, _ = strconv.Atoi(args[len(args)-1])
	switch args[2] {
	case "add":
		if _, ok := k[metric]; ok {
			return nil, fmt.Errorf("RTNETLINK answers: File exists")
		}
		k[metric] = proto.GatewayRoute{Interface: iface, Gateway: gateway, Metric: metric}
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
		if isGatewayInterface(k[metric].Interface) {
			entries = append(entries, k[metric])
		}
	}
	return entries
}

func (k kernelRoutes) metrics() map[int]bool {
	metrics := map[int]bool{}
	for metric := range k {
		metrics[metric] = true
	}
	return metrics
}

// useModel routes the commands and the table's metrics through table.
func useModel(t *testing.T, table kernelRoutes, run func(args ...string) ([]byte, error)) {
	t.Helper()
	previous, previousMetrics := gatewayRouteCommand, occupiedMetrics
	t.Cleanup(func() { gatewayRouteCommand, occupiedMetrics = previous, previousMetrics })
	gatewayRouteCommand = run
	occupiedMetrics = table.metrics
}

func TestPreferenceChangesKeepOneRoutePerGatewayThroughout(t *testing.T) {
	table := kernelRoutes{
		202: {Interface: "eth0", Gateway: "192.168.1.1", Metric: 202},
		303: {Interface: "wlan0", Gateway: "192.168.4.1", Metric: 303},
	}
	gateways := map[string]bool{"192.168.1.1": true, "192.168.4.1": true}
	useModel(t, table, func(args ...string) ([]byte, error) {
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
	})
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
	table := kernelRoutes{
		10:  {Interface: "wlan0", Gateway: "192.168.4.1", Metric: 10},
		202: {Interface: "eth0", Gateway: "192.168.1.1", Metric: 202},
		303: {Interface: "wlan0", Gateway: "192.168.4.1", Metric: 303},
	}
	useModel(t, table, table.run)
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
	table := kernelRoutes{202: {Interface: "eth0", Gateway: "192.168.1.1", Metric: 202}}
	useModel(t, table, func(args ...string) ([]byte, error) {
		if args[2] == "replace" || args[2] == "add" {
			return nil, fmt.Errorf("RTNETLINK answers: Network is unreachable")
		}
		return table.run(args...)
	})
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
	// A VPN at the first interim metric, which must survive.
	ip("link", "add", "tun0", "type", "dummy")
	ip("link", "set", "tun0", "up")
	ip("addr", "add", "198.51.100.2/24", "dev", "tun0")
	ip("route", "add", "default", "via", "198.51.100.1", "dev", "tun0", "metric", "1000")
	// USB networking at the metric Ethernet would prefer.
	ip("link", "add", "usb0", "type", "dummy")
	ip("link", "set", "usb0", "up")
	ip("addr", "add", "10.0.0.2/24", "dev", "usb0")
	ip("route", "add", "default", "via", "10.0.0.1", "dev", "usb0", "metric", "10")
	for _, preferred := range []string{"ethernet", "wifi", "ethernet", "auto", "wifi", "wifi", "ethernet", "auto"} {
		if err := applyGatewayPreference(preferred, defaultGatewayEntries()); err != nil {
			t.Fatalf("%s: %v", preferred, err)
		}
		entries := defaultGatewayEntries()
		out, _ := exec.Command("ip", "-4", "route", "show", "default").CombinedOutput()
		table := string(out)
		if len(entries) != 2 || !strings.Contains(table, "via 198.51.100.1 dev tun0 metric 1000") ||
			!strings.Contains(table, "via 10.0.0.1 dev usb0 metric 10") {
			t.Fatalf("%s: %+v\n%s", preferred, entries, table)
		}
		for _, route := range entries {
			want := preferenceMetric(preferred, route.Interface, map[string]int{"eth0": 0, "wlan0": 1}[route.Interface])
			// usb0 holds the 10 Ethernet would prefer: the next free one.
			if want == 10 {
				want = 11
			}
			if route.Metric != want {
				t.Fatalf("%s: %+v", preferred, entries)
			}
		}
	}
}
