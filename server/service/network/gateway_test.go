package network

import (
	"os"
	"path/filepath"
	"testing"
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
