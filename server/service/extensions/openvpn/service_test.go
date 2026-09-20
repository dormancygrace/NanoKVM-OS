package openvpn

import (
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
