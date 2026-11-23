package zerotier

import (
	"reflect"
	"testing"

	"NanoKVM-Server/proto"
)

func TestValidNetworkID(t *testing.T) {
	for value, want := range map[string]string{
		"8056c2e21c000001":   "8056c2e21c000001",
		" 8056C2E21C000001 ": "8056c2e21c000001",
	} {
		if got, ok := ValidNetworkID(value); !ok || got != want {
			t.Errorf("ValidNetworkID(%q) = %q, %t", value, got, ok)
		}
	}
	for _, value := range []string{"", "8056c2e21c00000", "8056c2e21c0000011", "8056c2e21c00000g", "-h",
		"8056c2e2 1c000001", "8056c2e21c000001;reboot", "8056c2e21c000001\nleave"} {
		if _, ok := ValidNetworkID(value); ok {
			t.Errorf("ValidNetworkID(%q) accepted", value)
		}
	}
}

func TestParseInfo(t *testing.T) {
	info, err := parseInfo(`{"address":"3d56351915","clock":1795345043724,"online":true,"tunneled":false,"version":"1.16.2","config":{"settings":{"primaryPort":9993}}}`)
	if err != nil || info.Address != "3d56351915" || !info.Online || info.Version != "1.16.2" {
		t.Fatalf("parseInfo = %+v, %v", info, err)
	}
	for _, output := range []string{"", "Error connecting to the ZeroTier service", `{"online":true}`} {
		if _, err := parseInfo(output); err == nil {
			t.Errorf("parseInfo(%q) accepted", output)
		}
	}
}

func TestParseNetworks(t *testing.T) {
	networks, err := parseNetworks(`[
 {"allowDNS":false,"allowDefault":false,"allowGlobal":false,"allowManaged":true,
  "assignedAddresses":["fd80:56c2:e21c::1/88","10.147.17.5/24"],"id":"8056c2e21c000001",
  "mac":"5e:00:00:00:00:01","mtu":2800,"name":"home","nwid":"8056c2e21c000001",
  "portDeviceName":"ztabcdefgh","status":"OK","type":"PRIVATE"},
 {"assignedAddresses":null,"id":"8056c2e21c000002","name":"","nwid":"8056c2e21c000002",
  "portDeviceName":"","status":"ACCESS_DENIED","type":"PRIVATE"}
]`)
	if err != nil {
		t.Fatal(err)
	}
	want := []proto.ZerotierNetwork{
		{ID: "8056c2e21c000001", Name: "home", Status: "OK", Type: "PRIVATE", Device: "ztabcdefgh",
			Addresses: []string{"fd80:56c2:e21c::1/88", "10.147.17.5/24"}},
		{ID: "8056c2e21c000002", Status: "ACCESS_DENIED", Type: "PRIVATE", Addresses: []string{}},
	}
	if !reflect.DeepEqual(networks, want) {
		t.Fatalf("parseNetworks = %+v", networks)
	}
	if got := firstIPv4(networks); got != "10.147.17.5" {
		t.Errorf("firstIPv4 = %q", got)
	}
	if got := firstIPv4(networks[1:]); got != "" {
		t.Errorf("firstIPv4 of a network that is not up = %q", got)
	}
	if networks, err := parseNetworks("[]"); err != nil || len(networks) != 0 {
		t.Errorf("parseNetworks([]) = %v, %v", networks, err)
	}
	if _, err := parseNetworks("200 listnetworks <nwid> <name>"); err == nil {
		t.Error("parseNetworks accepted text output")
	}
}
