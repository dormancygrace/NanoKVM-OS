package storageidentity

import (
	"os"
	"os/exec"
	"path/filepath"
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

// Run the production boot formatter as well as Go against the same fixtures.
func TestReadAndBootParity(t *testing.T) {
	source, err := os.ReadFile("../../../kvmapp/system/init.d/S03usbdev")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "storage_value() (")
	end := strings.Index(script, "start_usb_dev()")
	if start < 0 || end < start {
		t.Fatal("missing boot functions")
	}
	dir := t.TempDir()
	shells := [][]string{{"sh"}}
	if _, err := exec.LookPath("busybox"); err == nil {
		shells = append(shells, []string{"busybox", "sh"})
	}
	for _, value := range []string{"", "Generic", "Generic\n", "Generic\n\n", "01234567890123456", " padded", "tail ", "bad\tname", "é", "%s $(id)"} {
		for _, name := range Files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0644); err != nil {
				t.Fatal(err)
			}
		}
		got := Read(dir)
		if err := got.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, shell := range shells {
			for _, tc := range []struct{ field, kind, fallback string }{{"image", "disk", "USB Mass Storage"}, {"image", "cdrom", "USB CD/DVD-ROM"}, {"setup", "setup", "Setup Disk"}, {"user", "user", "User Disk"}} {
				cmd := exec.Command(shell[0], shell[1:]...)
				cmd.Stdin = strings.NewReader("conf='" + dir + "'\n" + script[start:end] + "\nstorage_inquiry " + tc.field + " '" + tc.fallback + "'\n")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %s", err, out)
				}
				if string(out) != got.Inquiry(tc.kind)+"\n" {
					t.Fatalf("%q %s: shell=%q Go=%q", value, tc.kind, out, got.Inquiry(tc.kind))
				}
			}
		}
	}
}
