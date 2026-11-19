package vm

import (
	"os"
	"path/filepath"
	"testing"
)

// A change between host and off that stops after its first write must not
// leave the gadget role, which would bind the composition at the next boot.
// The stop is forced here: the flag of the old role is a directory with an
// entry, which can be neither removed nor replaced, so the second write and
// the way back both fail.
func TestUSBRoleChangeCutShortStaysUnbound(t *testing.T) {
	for _, c := range []struct{ oldFlag, role string }{
		{usbHostFlag, usbRoleOff},
		{usbOffFlag, usbRoleHost},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, c.oldFlag, "keep"), 0o755); err != nil {
			t.Fatal(err)
		}
		store := usbRoleStore{
			dir:    dir,
			run:    func(string) error { return nil },
			verify: func(string) error { return nil },
		}
		if err := store.set(c.role); err == nil {
			t.Fatalf("%s to %s: the change was expected to fail", c.oldFlag, c.role)
		}
		if got := usbRoleFlags(dir); got == usbRoleGadget {
			t.Fatalf("%s to %s: the flags left on disk select the gadget role", c.oldFlag, c.role)
		}
	}
}
