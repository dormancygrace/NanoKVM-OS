package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInterfaceVisibilityUsesSavedPreference(t *testing.T) {
	boot, config := t.TempDir(), t.TempDir()
	// No carrier/address is required for a configured interface to stay visible.
	for _, name := range []string{"eth0", "eth0.10", "wlan0", "wg0"} {
		if !interfaceEnabled(name, boot, config) {
			t.Fatalf("enabled interface hidden: %s", name)
		}
	}
	if err := os.WriteFile(filepath.Join(boot, "eth.disabled"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"eth0", "eth0.10"} {
		if interfaceEnabled(name, boot, config) {
			t.Fatalf("disabled Ethernet visible: %s", name)
		}
	}
	if !interfaceEnabled("wlan0", boot, config) || !interfaceEnabled("wg0", boot, config) {
		t.Fatal("Ethernet preference affected unrelated interfaces")
	}
	if err := os.WriteFile(filepath.Join(config, "wifi.disabled"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if interfaceEnabled("wlan0", boot, config) {
		t.Fatal("disabled Wi-Fi visible")
	}
	if err := os.Remove(filepath.Join(boot, "eth.disabled")); err != nil {
		t.Fatal(err)
	}
	if !interfaceEnabled("eth0", boot, config) {
		t.Fatal("re-enabled Ethernet remains hidden")
	}
}
