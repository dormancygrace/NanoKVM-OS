package openvpn

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestDeviceNameIsStableAndFitsLinuxLimit(t *testing.T) {
	name := deviceName("ov0123456789")
	if name != "nv0123456789" || len(name) > 15 {
		t.Fatalf("unexpected device name %q", name)
	}
}

func TestRuntimeLogKeepsTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openvpn.log")
	prefix := make([]byte, 256*1024)
	for i := range prefix {
		prefix[i] = 'x'
	}
	if e := os.WriteFile(path, append(prefix, []byte("Initialization Sequence Completed\n")...), 0600); e != nil {
		t.Fatal(e)
	}
	log := runtimeLog(path)
	if len(log) > 256*1024 || lastIndexAny(log, "Initialization Sequence Completed") < 0 {
		t.Fatal("runtime log tail was not retained")
	}
}

func TestLastIndexAnyUsesNewestEvent(t *testing.T) {
	log := "Initialization Sequence Completed\nSIGUSR1\nRestart pause\n"
	reset := lastIndexAny(log, "SIGUSR1", "Restart pause")
	if reset <= lastIndexAny(log, "Initialization Sequence Completed") {
		t.Fatal("reconnect event must supersede an older successful connection")
	}
}

func TestDCOLinkJSONUsesKernelLinkKind(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{"ovpn", `[{"ifname":"nv0123456789","linkinfo":{"info_kind":"ovpn"}}]`, true},
		{"legacy-name", `[{"ifname":"ovpn-looking-tun","linkinfo":{"info_kind":"tun","info_data":{"type":"tun"}}}]`, false},
		{"missing-kind", `[{"ifname":"nv0123456789"}]`, false},
		{"multiple", `[{"linkinfo":{"info_kind":"ovpn"}},{"linkinfo":{"info_kind":"tun"}}]`, false},
		{"invalid", `not-json`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dcoLinkJSON([]byte(test.data)); got != test.want {
				t.Fatalf("dcoLinkJSON() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDCOKindIsReadOncePerInterface(t *testing.T) {
	previous := linkDetail
	defer func() { linkDetail = previous }()
	runs := 0
	kind, failure := "ovpn", error(nil)
	linkDetail = func(name string) ([]byte, error) {
		runs++
		return []byte(`[{"ifname":"` + name + `","linkinfo":{"info_kind":"` + kind + `"}}]`), failure
	}
	s := &Service{}
	tunnel := &net.Interface{Name: "nv0123456789", Index: 7}
	if !s.dcoInterface(tunnel) || !s.dcoInterface(tunnel) || runs != 1 {
		t.Fatalf("DCO link read %d times", runs)
	}
	// A restarted tunnel is a new interface, whatever its kind now.
	kind = "tun"
	if s.dcoInterface(&net.Interface{Name: tunnel.Name, Index: 8}) || runs != 2 {
		t.Fatalf("new interface index not read again (%d runs)", runs)
	}
	failure = errors.New("ip failed")
	if s.dcoInterface(&net.Interface{Name: tunnel.Name, Index: 9}) || s.dcoInterface(&net.Interface{Name: tunnel.Name, Index: 9}) || runs != 4 {
		t.Fatalf("a failed read was kept (%d runs)", runs)
	}
}

func TestFirstIPv4SkipsIPv6(t *testing.T) {
	_, v6, _ := net.ParseCIDR("fd00::2/64")
	addrs := []net.Addr{v6, &net.IPNet{IP: net.IPv4(10, 8, 0, 6), Mask: net.CIDRMask(32, 32)}, &net.IPNet{IP: net.IPv4(10, 9, 0, 2), Mask: net.CIDRMask(24, 32)}}
	if got := firstIPv4(addrs); got != "10.8.0.6" {
		t.Fatalf("firstIPv4() = %q", got)
	}
	if got := firstIPv4(addrs[:1]); got != "" {
		t.Fatalf("IPv6-only interface reported %q", got)
	}
}

func TestInterfaceAddressReadsTheKernel(t *testing.T) {
	loopback, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skip("no loopback interface")
	}
	if got := interfaceAddress(loopback); got != "127.0.0.1" {
		t.Fatalf("interfaceAddress(lo) = %q", got)
	}
}
