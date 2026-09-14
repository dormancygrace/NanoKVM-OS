package osupdate

import "testing"

func TestVersionedReleaseAssets(t *testing.T) {
	for _, tt := range []struct{ tag, name string }{
		{"v1.0.0-beta.10", "NanoKVM-OS-v1.0.0-beta-10.nkos"},
		{"v1.0.0-beta.11", "NanoKVM-OS-v1.0.0-beta-11.nkos"},
		{"v1.0.0-rc.1", "NanoKVM-OS-v1.0.0-rc-1.nkos"},
		{"v1.0.0", "NanoKVM-OS-v1.0.0.nkos"},
	} {
		raw := "https://github.com/" + Repository + "/releases/download/" + tt.tag + "/" + tt.name
		if !releaseAssetMatches(tt.tag, tt.name) || !ValidAssetURL(raw) {
			t.Errorf("rejected %s", raw)
		}
	}
}

func TestReleaseAssetCannotCrossTagOrPath(t *testing.T) {
	for _, path := range []string{
		"v1.0.0-beta.10/NanoKVM-OS-v1.0.0-beta-11.nkos",
		"v1.0.0-beta.10/NanoKVM-OS-1.0.0-beta.10.nkos",
		"v1.0.0-beta.10/extra/NanoKVM-OS-v1.0.0-beta-10.nkos",
		"../NanoKVM-OS-update.nkos",
		"v1.0.0-beta-10/NanoKVM-OS-v1.0.0-beta-10.nkos",
		"v1.0.0-beta.10/NanoKVM-OS-v1.0.0-beta-10.nkos?token=unexpected",
	} {
		if ValidAssetURL("https://github.com/" + Repository + "/releases/download/" + path) {
			t.Errorf("accepted %s", path)
		}
	}
}
