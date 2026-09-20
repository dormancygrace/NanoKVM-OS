package network

import "testing"

func TestParseDefaultGatewayRoutesOnlyReturnsPhysicalInterfaces(t *testing.T) {
	routes := parseDefaultGatewayRoutes("default via 192.168.1.1 dev eth0 proto dhcp metric 100\ndefault via 10.0.0.1 dev wlan0 proto dhcp metric 200\ndefault dev tun0 scope link metric 50\ndefault via 192.168.1.1 dev eth0 proto dhcp metric 100\n")
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}
	if routes[0].Interface != "eth0" || routes[0].Gateway != "192.168.1.1" || routes[0].Metric != 100 {
		t.Fatalf("unexpected Ethernet route: %+v", routes[0])
	}
	if routes[1].Interface != "wlan0" || routes[1].Gateway != "10.0.0.1" || routes[1].Metric != 200 {
		t.Fatalf("unexpected Wi-Fi route: %+v", routes[1])
	}
}
