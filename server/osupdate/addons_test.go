package osupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestParseAddonIndexCoreFromRelease(t *testing.T) {
	// An OS package unknown to this server, named by the indexed release.
	input := strings.Replace(addonIndexFixture, "\n]", `,
  {"name": "nanokvm-future-core", "version": "1.0-r0"}
]`, 1)
	input = strings.Replace(input, `"alpine-base"`, `"alpine-base", "nanokvm-future-core>=1.0-r0"`, 1)
	stored, err := parseAddonIndex(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, addon := range stored.Addons {
		if addon.Name == "nanokvm-future-core" {
			t.Error("OS package saved as an add-on")
		}
	}
	if !reflect.DeepEqual(stored.Core, []string{"nanokvm", "nanokvm-base", "nanokvm-future-core", "nanokvm-release", "nanokvm-web"}) {
		t.Fatalf("core=%q", stored.Core)
	}
	if len(stored.Addons) != 2 {
		t.Fatalf("real add-ons were lost: %+v", stored.Addons)
	}
}

// The installed release decides with the index which packages are the OS: a
// cached index of an older release lists an OS package added since as an
// add-on, and a package the installed release moved out of the OS is an add-on
// although this server's built-in list still names it.
func TestAddonCatalogUsesInstalledRelease(t *testing.T) {
	addonFixture(t, "P:nanokvm-release\nV:3.7.0-r0\nD:alpine-base nanokvm>=3.7.0-r0 nanokvm-openssl>=3.7.0-r0 nanokvm-web>=3.7.0-r0\n\n"+
		"P:nanokvm-openssl\nV:3.7.0-r0\n\nP:nanokvm-i2c-tools\nV:3.7.0-r0\n\nP:nanokvm-web\nV:3.7.0-r0\n\n")
	stored, err := parseAddonIndex(strings.Replace(addonIndexFixture, "\n]", `,
  {"name": "nanokvm-openssl", "version": "3.6.0-r0"},
  {"name": "nanokvm-i2c-tools", "version": "3.7.0-r0"}
]`, 1))
	if err != nil {
		t.Fatal(err)
	}
	writeStored(t, stored)
	if IsInstalledAddon("nanokvm-openssl") || IsInstalledAddon("nanokvm-web") {
		t.Fatal("an OS package of the installed release is removable as an add-on")
	}
	if _, err := AddonInstallSpec("nanokvm-openssl"); err == nil {
		t.Fatal("an OS package of the installed release is installable as an add-on")
	}
	if !IsInstalledAddon("nanokvm-i2c-tools") {
		t.Fatal("a package the installed release moved out of the OS is not an add-on")
	}
}

func addonFixture(t *testing.T, installed string) {
	t.Helper()
	noticeFixture(t, noticeRepositories)
	oldDB := installedDBPath
	t.Cleanup(func() { installedDBPath = oldDB })
	installedDBPath = filepath.Join(t.TempDir(), "installed")
	if err := os.WriteFile(installedDBPath, []byte(installed), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestAddonCatalog(t *testing.T) {
	addonFixture(t, "P:nanokvm-release\nV:3.5.0-r0\n\nP:nanokvm-rustdesk\nV:0.5.4-r0\n\nP:nanokvm-web\nV:3.5.0-r0\n\n"+
		"P:nanokvm-gone\nV:1.0-r0\n\n")
	// Before the first check since boot: the OS and the installed add-ons.
	if c := GetAddonCatalog(); c.OS != "3.5.0" || c.CheckedAt != 0 || len(c.Addons) != 2 ||
		c.Addons[0].Name != "nanokvm-gone" || c.Addons[1].Name != "nanokvm-rustdesk" || c.Addons[1].Version != "" {
		t.Fatalf("catalog before a check=%+v", c)
	}
	if _, err := AddonInstallSpec("nanokvm-rustdesk"); err == nil {
		t.Fatal("installed an add-on missing from the list")
	}
	// knownCore may lack an OS package of a later release: before the first
	// list nothing is removable as an add-on.
	if IsInstalledAddon("nanokvm-gone") {
		t.Fatal("an add-on is removable before the first list")
	}
	stored, err := parseAddonIndex(addonIndexFixture)
	if err != nil {
		t.Fatal(err)
	}
	writeStored(t, stored)
	c := GetAddonCatalog()
	if c.CheckedAt != 42 || len(c.Addons) != 3 {
		t.Fatalf("catalog=%+v", c)
	}
	ipmi, rustdesk, gone := c.Addons[1], c.Addons[2], c.Addons[0]
	if ipmi.Name != "nanokvm-ipmi" || ipmi.Compatible || ipmi.Installed != "" {
		t.Fatalf("3.6.0_beta1 add-on on 3.5.0: %+v", ipmi)
	}
	if rustdesk.Installed != "0.5.4-r0" || !rustdesk.Compatible {
		t.Fatalf("rustdesk=%+v", rustdesk)
	}
	// Installed, no longer offered: listed so that it can be removed.
	if gone.Name != "nanokvm-gone" || gone.Version != "" || gone.Installed != "1.0-r0" {
		t.Fatalf("gone=%+v", gone)
	}
	if _, err := AddonInstallSpec("nanokvm-ipmi"); err == nil || !strings.Contains(err.Error(), "3.6.0_beta1") {
		t.Fatalf("minimum release not enforced: %v", err)
	}
	for _, name := range []string{"nanokvm-gone", "nanokvm-web", "zlib"} {
		if _, err := AddonInstallSpec(name); err == nil {
			t.Errorf("AddonInstallSpec(%s) passed", name)
		}
	}
	if spec, err := AddonInstallSpec("nanokvm-rustdesk"); err != nil || spec != "nanokvm-rustdesk>=0.5.5-r0" {
		t.Fatalf("install spec=%q, error=%v", spec, err)
	}
	if !IsInstalledAddon("nanokvm-gone") || !IsInstalledAddon("nanokvm-rustdesk") || IsInstalledAddon("nanokvm-ipmi") ||
		IsInstalledAddon("nanokvm-web") || IsInstalledAddon("nanokvm-release") {
		t.Fatal("IsInstalledAddon")
	}
	// The stable release satisfies a beta minimum.
	addonFixture(t, "P:nanokvm-release\nV:3.6.0-r0\n\n")
	writeStored(t, stored)
	if _, err := AddonInstallSpec("nanokvm-ipmi"); err != nil {
		t.Fatal(err)
	}
}

func TestAddonCatalogKeepsKnownCoreWithStaleCache(t *testing.T) {
	for _, cachedAsAddon := range []bool{false, true} {
		name := "old core list"
		if cachedAsAddon {
			name = "misclassified cached add-ons"
		}
		t.Run(name, func(t *testing.T) {
			addonFixture(t, "P:nanokvm-release\nV:3.6.0-r0\n\n"+
				"P:nanokvm-openssl\nV:3.6.0-r0\n\nP:nanokvm-web\nV:3.6.0-r0\n\n"+
				"P:nanokvm-future-core\nV:1.0-r0\n\n"+
				"P:nanokvm-rustdesk\nV:0.5.6-r0\n\nP:nanokvm-gone\nV:1.0-r0\n\n")
			stored := storedCatalog{
				Core:   []string{"nanokvm-release", "nanokvm-future-core"},
				Addons: []Addon{{Name: "nanokvm-rustdesk", Version: "0.5.6-r0"}},
			}
			core := []string{"nanokvm-openssl", "nanokvm-web", "nanokvm-future-core"}
			if cachedAsAddon {
				for _, name := range core {
					stored.Addons = append(stored.Addons, Addon{Name: name, Version: "3.6.0-r0"})
				}
			}
			writeStored(t, stored)
			want := []Addon{
				{Name: "nanokvm-gone", Installed: "1.0-r0", Compatible: true},
				{Name: "nanokvm-rustdesk", Version: "0.5.6-r0", Installed: "0.5.6-r0", Compatible: true},
			}
			catalog := GetAddonCatalog()
			if catalog.CheckedAt != 42 || !reflect.DeepEqual(catalog.Addons, want) {
				t.Fatalf("catalog with stale cache=%+v", catalog)
			}
			for _, name := range core {
				if _, err := AddonInstallSpec(name); err == nil {
					t.Errorf("system package %s accepted for add-on installation", name)
				}
				if IsInstalledAddon(name) {
					t.Errorf("system package %s accepted for add-on removal", name)
				}
			}
			if !IsInstalledAddon("nanokvm-gone") || !IsInstalledAddon("nanokvm-rustdesk") {
				t.Fatal("real installed add-ons cannot be removed")
			}
			if spec, err := AddonInstallSpec("nanokvm-rustdesk"); err != nil || spec != "nanokvm-rustdesk>=0.5.6-r0" {
				t.Fatalf("real add-on installation: spec=%q, error=%v", spec, err)
			}
		})
	}
}

func writeStored(t *testing.T, stored storedCatalog) {
	t.Helper()
	stored.CheckedAt = 42
	b, err := json.Marshal(stored)
	if err == nil {
		err = os.MkdirAll(apkStateDir, 0700)
	}
	if err == nil {
		err = os.WriteFile(addonCatalogPath(), b, 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestCoreChanges(t *testing.T) {
	addonFixture(t, "")
	stored, _ := parseAddonIndex(addonIndexFixture)
	writeStored(t, stored)
	simulation := `(1/4) Installing ipmitool (1.8.19-r2)
(2/4) Upgrading nanokvm-base (3.4.0-r0 -> 3.6.0-r0)
(3/4) Upgrading nanokvm-release (3.5.0-r0 -> 3.6.0-r0)
(4/4) Installing nanokvm-ipmi (0.2.0-r1)
OK: 124 MiB in 171 packages`
	if got := CoreChanges(simulation, "nanokvm-ipmi"); !reflect.DeepEqual(got, []string{"nanokvm-base", "nanokvm-release"}) {
		t.Fatalf("changes=%q", got)
	}
	// Its own upgrade and other Alpine packages are fine.
	if got := CoreChanges("(1/2) Upgrading nanokvm-ipmi (0.1.0-r0 -> 0.2.0-r1)\n(2/2) Upgrading musl (1-r0 -> 2-r0)\n", "nanokvm-ipmi"); len(got) != 0 {
		t.Fatalf("changes=%q", got)
	}
}

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
