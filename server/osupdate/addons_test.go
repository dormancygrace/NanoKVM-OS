package osupdate

import (
	"reflect"

	"testing"
)

// An `apk query --format json` listing of nanokvm-*: the OS packages, an old
// and a new IPMI, RustDesk without a minimum release, and a warning first.
const addonIndexFixture = `WARNING: opening cache: No such file or directory
[
  {"name": "nanokvm-release", "version": "3.5.0-r0", "depends": ["alpine-base", "nanokvm>=3.5.0-r0", "nanokvm-web>=3.5.0-r0", "nanokvm-base>=3.4.0-r0"]},
  {"name": "nanokvm", "version": "3.5.0-r0", "depends": ["nanokvm-base>=3.0_alpha1-r1"]},
  {"name": "nanokvm-web", "version": "3.5.0-r0"},
  {"name": "nanokvm-base", "version": "3.4.0-r0"},
  {"name": "nanokvm-ipmi", "version": "0.1.0-r0", "description": "old", "depends": ["openrc", "nanokvm-os>=3.5.0"]},
  {"name": "nanokvm-ipmi", "version": "0.2.0-r1", "description": "NanoKVM IPMI add-on", "url": "https://example.test/bmc",
   "license": "GPL-3.0-only", "depends": ["openrc", "nanokvm-os>=3.6.0_beta1"]},
  {"name": "nanokvm-rustdesk", "version": "0.5.5-r0", "description": "RustDesk", "depends": ["nanokvm-rustdesk-bridge=1", "openrc"]}
]`

func TestParseAddonIndex(t *testing.T) {
	stored, err := parseAddonIndex(addonIndexFixture)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"nanokvm", "nanokvm-base", "nanokvm-release", "nanokvm-web"}; !reflect.DeepEqual(stored.Core, want) {
		t.Fatalf("core=%q", stored.Core)
	}
	want := []Addon{
		{Name: "nanokvm-ipmi", Version: "0.2.0-r1", Description: "NanoKVM IPMI add-on", URL: "https://example.test/bmc",
			License: "GPL-3.0-only", MinOS: "3.6.0_beta1"},
		{Name: "nanokvm-rustdesk", Version: "0.5.5-r0", Description: "RustDesk"},
	}
	if !reflect.DeepEqual(stored.Addons, want) {
		t.Fatalf("add-ons=%+v", stored.Addons)
	}
	for _, bad := range []string{"", "ERROR: no index", `[{"name": "nanokvm-ipmi", "version": "1-r0"}]`} {
		if _, err := parseAddonIndex(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// An OS package unknown to this server, named by the indexed release.

// The installed release decides with the index which packages are the OS: a
// cached index of an older release lists an OS package added since as an
// add-on, and a package the installed release moved out of the OS is an add-on
// although this server's built-in list still names it.

// Offline after a boot, the installed release alone decides.

// Before the first check since boot: the OS and the installed add-ons.

// knownCore may lack an OS package of a later release: before the first
// list nothing is removable as an add-on.

// Installed, no longer offered: listed so that it can be removed.

// The stable release satisfies a beta minimum.

// Its own upgrade and other Alpine packages are fine.

func TestCompareAPKVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"3.5.0", "3.5.0", 0},
		{"3.5.0", "3.4.9", 1},
		{"3.10.0", "3.9.0", 1},
		{"3.5.0_beta1", "3.5.0", -1},
		{"3.5.0_beta2", "3.5.0_beta10", -1},
		{"3.5.0_rc1", "3.5.0_beta3", 1},
		{"3.5.0", "3.5", 1},
		{"3.5.0-r1", "3.5.0-r0", 1},
		{"3.5.0_p1", "3.5.0", 1},
		{"1.0a", "1.0", 1},
		{"3.0_alpha1-r1", "3.0-r0", -1},
	} {
		if got := compareAPKVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s: %d, want %d", c.a, c.b, got, c.want)
		}
		if got := compareAPKVersions(c.b, c.a); got != -c.want {
			t.Errorf("%s vs %s: %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}
