package recovery

import "testing"

// The ZeroTier identity and its networks are backed up, restored on the same
// device and never cloned: two devices with one identity break each other.
func TestZerotierStatePolicy(t *testing.T) {
	for _, p := range []string{
		"var/lib/zerotier-one/identity.secret", "var/lib/zerotier-one/identity.public",
		"var/lib/zerotier-one/local.conf", "var/lib/zerotier-one/networks.d/8056c2e21c000001.conf",
		"var/lib/zerotier-one/networks.d/8056c2e21c000001.local.conf",
	} {
		if !allowed(p) {
			t.Errorf("%s not in the backup", p)
		}
		if portable(p) {
			t.Errorf("%s copied to another device", p)
		}
	}
	// The API token, caches and the planet file are not settings.
	for _, p := range []string{
		"var/lib/zerotier-one/authtoken.secret", "var/lib/zerotier-one/peers.d/1234567890.peer",
		"var/lib/zerotier-one/planet", "var/lib/zerotier-one/zerotier-one.pid",
	} {
		if allowed(p) {
			t.Errorf("%s in the backup", p)
		}
	}
}
