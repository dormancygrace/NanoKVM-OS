package openvpn

import (
	"testing"
)

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
