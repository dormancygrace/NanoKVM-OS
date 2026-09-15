package network

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWolInterfacesAndSelection(t *testing.T) {
	original := systemNetworkInterfaces
	originalSysClassNet := wolSysClassNet
	wolSysClassNet = t.TempDir()
	for _, item := range []struct {
		name   string
		uevent string
	}{
		{name: "eth0", uevent: "INTERFACE=eth0\nIFINDEX=2\n"},
		{name: "wlan0", uevent: "DEVTYPE=wlan\nINTERFACE=wlan0\nIFINDEX=4\n"},
		{name: "usb0", uevent: "DEVTYPE=gadget\nINTERFACE=usb0\nIFINDEX=7\n"},
	} {
		base := filepath.Join(wolSysClassNet, item.name)
		if err := os.MkdirAll(filepath.Join(base, "device"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "uevent"), []byte(item.uevent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	systemNetworkInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Name: "wlan0", HardwareAddr: net.HardwareAddr{0, 1, 2, 3, 4, 5}, Flags: net.FlagUp | net.FlagBroadcast},
			{Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
			{Name: "eth0", HardwareAddr: net.HardwareAddr{6, 7, 8, 9, 10, 11}, Flags: net.FlagUp | net.FlagBroadcast},
			{Name: "usb0", HardwareAddr: net.HardwareAddr{6, 7, 8, 9, 10, 13}, Flags: net.FlagUp | net.FlagBroadcast},
			{Name: "down0", HardwareAddr: net.HardwareAddr{6, 7, 8, 9, 10, 12}, Flags: net.FlagBroadcast},
			{Name: "tun0", Flags: net.FlagUp},
		}, nil
	}
	t.Cleanup(func() {
		systemNetworkInterfaces = original
		wolSysClassNet = originalSysClassNet
	})

	interfaces, err := wolInterfaces()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(interfaces, []string{"eth0", "wlan0"}) {
		t.Fatalf("interfaces = %#v", interfaces)
	}
	if selected, err := selectWolInterface(""); err != nil || selected != "eth0" {
		t.Fatalf("default interface = %q, %v", selected, err)
	}
	if selected, err := selectWolInterface("wlan0"); err != nil || selected != "wlan0" {
		t.Fatalf("selected interface = %q, %v", selected, err)
	}
	for _, invalid := range []string{"lo", "usb0", "down0", "wlan0;reboot", " wlan0 "} {
		if _, err := selectWolInterface(invalid); err == nil && invalid != " wlan0 " {
			t.Errorf("accepted invalid interface %q", invalid)
		}
	}
}

func TestWolCommandUsesSeparateArguments(t *testing.T) {
	cmd := wolCommand("wlan0", "AA:BB:CC:DD:EE:FF")
	want := []string{"ether-wake", "-i", "wlan0", "-b", "AA:BB:CC:DD:EE:FF"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("command argv = %#v", cmd.Args)
	}
}
