package storageidentity

import (
	"strings"
	"testing"
)

func TestValidationAndFormatting(t *testing.T) {
	for _, kind := range []string{"disk", "cdrom", "remote", "setup", "user"} {
		got := (Identity{}).Inquiry(kind)
		if len(got) != 28 || got[:8] != "NanoKVM " || got[24:] != "0520" {
			t.Fatalf("%s: %q", kind, got)
		}
	}
	for _, bad := range []Identity{{Vendor: "123456789"}, {Image: strings.Repeat("x", 17)}, {Setup: " bad"}, {User: "bad\nname"}, {Image: "é"}, {Vendor: "bad\x00"}} {
		if bad.Validate() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	custom := Identity{Vendor: "ACME", Image: "%s $(id)", Setup: "Setup", User: "Files"}
	if err := custom.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"disk", "cdrom", "remote"} {
		if got := custom.Inquiry(kind); got != "ACME    %s $(id)        0520" {
			t.Fatalf("%q", got)
		}
	}
}
