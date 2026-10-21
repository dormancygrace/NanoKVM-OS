package usbinternet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUSBFlagsLayoutActivation(t *testing.T) {
	for _, app := range []bool{false, true} {
		for _, active := range []bool{false, true} {
			root := t.TempDir()
			a, b := filepath.Join(root, "app"), filepath.Join(root, "active")
			for path, present := range map[string]bool{a: app, b: active} {
				if present {
					if err := os.WriteFile(path, nil, 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			want := "/boot"
			if app && active {
				want = "/etc/kvm/usb"
			}
			if got := usbFlagsPath(a, b); got != want {
				t.Fatalf("app=%v active=%v: got %s, want %s", app, active, got, want)
			}
		}
	}
}
