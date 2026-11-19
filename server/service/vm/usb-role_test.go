package vm

import (
	"path/filepath"
	"testing"
)

// A change between host and off can stop after any of its writes. What is
// on disk then must not be the gadget role, which would bind the composition
// at the next boot.
func TestUSBRoleChangeNeverPassesThroughGadget(t *testing.T) {
	for _, c := range []struct{ from, to string }{
		{usbRoleHost, usbRoleOff},
		{usbRoleOff, usbRoleHost},
	} {
		dir := t.TempDir()
		apply := func(flag usbFlagWrite) {
			t.Helper()
			if err := writeUSBFile(filepath.Join(dir, flag.name), usbFileSnapshot{exists: flag.exists, mode: 0o644}); err != nil {
				t.Fatal(err)
			}
		}
		for _, flag := range usbRoleWrites(c.from) {
			apply(flag)
		}
		for step, flag := range usbRoleWrites(c.to) {
			apply(flag)
			if got := usbRoleFlags(dir); got == usbRoleGadget {
				t.Fatalf("%s to %s: gadget after write %d", c.from, c.to, step+1)
			}
		}
		if got := usbRoleFlags(dir); got != c.to {
			t.Fatalf("%s to %s: ended as %s", c.from, c.to, got)
		}
	}
}
