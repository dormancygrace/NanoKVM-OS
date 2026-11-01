package osupdate

import (
	"testing"
)

func TestSoftwareValidationAndParsing(t *testing.T) {
	for _, name := range []string{"tailscale", "foo+bar", "pkg_1.2"} {
		if err := validPackageName(name); err != nil {
			t.Fatalf("valid package %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", "../apk", "name;id", "with space"} {
		if err := validPackageName(name); err == nil {
			t.Fatalf("invalid package %q accepted", name)
		}
	}
	if err := checkRemovable("nanokvm-app"); err == nil {
		t.Fatal("protected package accepted for removal")
	}
	got := parsePackages("zlib-1.3.1-r0 - compression\nnanokvm-app-2.0_alpha2-r3\nzlib-1.3.1-r0\n", 50)
	if len(got) != 2 || got[0].Name != "nanokvm-app" || got[1].Version != "1.3.1-r0" {
		t.Fatalf("unexpected packages: %#v", got)
	}
	matching := parseMatchingPackages("cloud-init-datasource-ibmcloud-26.1-r3 - cloud\nmc-4.8.33-r3 - file manager\nmcabber-1.1.2-r6 - console client\n", "mc", 50)
	if len(matching) != 3 || matching[0].Name != "mc" || matching[1].Name != "mcabber" || matching[2].Name != "cloud-init-datasource-ibmcloud" {
		t.Fatalf("unexpected search matches: %#v", matching)
	}
	updates := parseSoftwareUpdates("Installed: Available:\nalpha-1.0-r0 < alpha-1.1-r0\nbeta-2.0-r0 beta-2.1-r0\n")
	if len(updates) != 2 || updates[0] != (SoftwareUpdate{Name: "alpha", Installed: "1.0-r0", Available: "1.1-r0"}) || updates[1] != (SoftwareUpdate{Name: "beta", Installed: "2.0-r0", Available: "2.1-r0"}) {
		t.Fatalf("unexpected package updates: %#v", updates)
	}
	// apk-tools 3.0.8 as it prints on the device: the available version alone.
	updates = parseSoftwareUpdates("Installed:                                Available:\n" +
		"nanokvm-app-3.0_alpha1-r0               < 3.1.0-r0 \nzlib-1.3.2-r0                           < 1.3.2-r1 \n")
	if len(updates) != 2 || updates[0] != (SoftwareUpdate{Name: "nanokvm-app", Installed: "3.0_alpha1-r0", Available: "3.1.0-r0"}) ||
		updates[1] != (SoftwareUpdate{Name: "zlib", Installed: "1.3.2-r0", Available: "1.3.2-r1"}) {
		t.Fatalf("unexpected apk 3 package updates: %#v", updates)
	}
	// Another package's name-version is not this package's update.
	if updates = parseSoftwareUpdates("alpha-1.0-r0 < beta-1.1-r0\n"); len(updates) != 0 {
		t.Fatalf("took another package's version: %#v", updates)
	}
}

func TestSplitApplicationPackagesProtected(t *testing.T) {
	for _, name := range []string{"nanokvm", "nanokvm-web", "nanokvm-app", "nanokvm-openssl"} {
		if err := checkRemovable(name); err == nil {
			t.Fatalf("%s must remain protected", name)
		}
	}
}
