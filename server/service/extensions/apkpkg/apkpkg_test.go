package apkpkg

import "testing"

func TestSelected(t *testing.T) {
	world := []byte("nanokvm-release\nwireguard-tools-wg\nopenvpn>=2.6\nnanokvm-web><Q1abc=\ntailscale@community\n")
	for name, want := range map[string]bool{"nanokvm-release": true, "openvpn": true, "nanokvm-web": true, "tailscale": true, "wireguard-tools": false, "nanokvm": false} {
		if selected(world, name) != want {
			t.Errorf("%s: want %v", name, want)
		}
	}
}
