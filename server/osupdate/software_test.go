package osupdate

import (
	"os"
	"path/filepath"
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
}

func TestRunSoftware(t *testing.T) {
	oldDir, oldExec := apkStateDir, apkExecutable
	defer func() { apkStateDir, apkExecutable = oldDir, oldExec }()
	apkStateDir = t.TempDir()
	apkExecutable = filepath.Join(t.TempDir(), "apk")
	if err := os.WriteFile(apkExecutable, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RunSoftware("install", "tailscale"); err != nil {
		t.Fatal(err)
	}
	status := GetSoftwareStatus()
	if status.State != "succeeded" || status.Action != "install" || status.Package != "tailscale" {
		t.Fatalf("unexpected status: %#v", status)
	}
}
