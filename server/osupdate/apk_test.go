package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeAPKWorker(t *testing.T) {
	oldDir, oldExec, oldWorld, oldDB := apkStateDir, apkExecutable, worldPath, installedDBPath
	defer func() { apkStateDir, apkExecutable, worldPath, installedDBPath = oldDir, oldExec, oldWorld, oldDB }()
	apkStateDir = t.TempDir()
	apkExecutable = filepath.Join(t.TempDir(), "apk")
	worldPath = filepath.Join(apkStateDir, "world")
	installedDBPath = filepath.Join(apkStateDir, "installed")
	os.WriteFile(installedDBPath, []byte("P:nanokvm-kernel\nV:2.0_alpha1-r1\n\n"), 0600)
	install := func(script string) {
		t.Helper()
		if e := os.WriteFile(apkExecutable, []byte("#!/bin/sh\n"+script), 0700); e != nil {
			t.Fatal(e)
		}
	}
	install(`case "$1" in update) exit 0;; version) printf 'Installed: Available:\nnanokvm-kernel-2.0_alpha1-r1 < 2.0_alpha2-r0\n';; upgrade) exit 0;; *) exit 99;; esac
`)
	if e := RunAPK("check"); e != nil {
		t.Fatal(e)
	}
	if s := GetAPKStatus(); s.State != "ready" || s.Upgrades == "" {
		t.Fatal(s)
	}
	if e := RunAPK("install"); e != nil {
		t.Fatal(e)
	}
	if s := GetAPKStatus(); s.State != "installed" {
		t.Fatal(s)
	}
	install("exit 1\n")
	if e := RunAPK("install"); e == nil {
		t.Fatal("failed index refresh accepted")
	}
	if s := GetAPKStatus(); s.State != "failed" {
		t.Fatal(s)
	}
	if e := StartAPK("invalid"); e == nil {
		t.Fatal("unrecognized action accepted")
	}
}

// A package installed from a local file is selected as that file: installing
// updates takes a NanoKVM one back to the repositories before upgrading, and
// another one is not offered, since apk upgrade keeps it.
func TestLocalFileSelections(t *testing.T) {
	dir := t.TempDir()
	oldDir, oldExec, oldWorld, oldDB := apkStateDir, apkExecutable, worldPath, installedDBPath
	t.Cleanup(func() { apkStateDir, apkExecutable, worldPath, installedDBPath = oldDir, oldExec, oldWorld, oldDB })
	apkStateDir, apkExecutable, worldPath = dir, filepath.Join(dir, "apk"), filepath.Join(dir, "world")
	installedDBPath = filepath.Join(dir, "installed")
	installed := func(rustdesk string) {
		t.Helper()
		db := "P:nanokvm-rustdesk\nV:" + rustdesk + "\n\nP:nanokvm-app\nV:3.6.0-r0\n\nP:tailscale\nV:1.90.1-r0\n\n" +
			"P:busybox\nV:1.37.0-r30\n\nP:netbird\nV:0.78.2-r0\n\n"
		if err := os.WriteFile(installedDBPath, []byte(db), 0600); err != nil {
			t.Fatal(err)
		}
	}
	installed("0.5.5-r0")
	world := "busybox=1.37.0-r30\nnanokvm-app\nnanokvm-rustdesk><Q15uLgzMeQQlIv4B5ols+nasIpK7s=\ntailscale><Q1abc=\nnetbird>=0.78.2-r0\n"
	if err := os.WriteFile(worldPath, []byte(world), 0600); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\ncase \"$1\" in update|add|upgrade) exit 0;;\n" +
		"version) printf 'Installed:                                Available:\\n" +
		"nanokvm-rustdesk-0.5.5-r0               < 0.5.6-r0 \\ntailscale-1.90.1-r0                     < 1.92.0-r0 \\n" +
		"nanokvm-app-3.6.0-r0                    < 3.6.1-r0 \\nbusybox-1.37.0-r30                      < 1.37.0-r31 \\n" +
		"netbird-0.78.2-r0                       < 0.79.0-r0 \\n';;\n*) exit 99;;\nesac\n"
	if err := os.WriteFile(apkExecutable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RunAPK("check"); err != nil {
		t.Fatal(err)
	}
	want := "nanokvm-rustdesk-0.5.5-r0               < 0.5.6-r0\nnanokvm-app-3.6.0-r0                    < 3.6.1-r0\n" +
		"netbird-0.78.2-r0                       < 0.79.0-r0"
	if s := GetAPKStatus(); s.State != "ready" || s.Upgrades != want {
		t.Fatalf("%+v", s)
	}
	// Updated since the check, from a shell: no longer listed.
	installed("0.5.6-r0")
	if s := GetAPKStatus(); s.State != "ready" || s.Upgrades != "nanokvm-app-3.6.0-r0                    < 3.6.1-r0\nnetbird-0.78.2-r0                       < 0.79.0-r0" {
		t.Fatalf("%+v", s)
	}
	installed("0.5.5-r0")
	os.Remove(calls)
	if err := RunAPK("install"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(calls); string(data) != "update\nadd nanokvm-rustdesk\nupgrade\n" {
		t.Fatalf("apk calls:\n%s", data)
	}
	// Nothing pinned: no extra step.
	if err := os.WriteFile(worldPath, []byte("busybox\nnanokvm-app\n"), 0600); err != nil {
		t.Fatal(err)
	}
	os.Remove(calls)
	if err := RunAPK("install"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(calls); string(data) != "update\nupgrade\n" {
		t.Fatalf("apk calls:\n%s", data)
	}
}

// The notice is a snapshot of the last check: a package updated since then
// is no longer offered, and the components show no update for it.
func TestNoticeLeavesOutPackagesUpdatedSinceTheCheck(t *testing.T) {
	dir := t.TempDir()
	oldDir, oldDB := apkStateDir, installedDBPath
	t.Cleanup(func() { apkStateDir, installedDBPath = oldDir, oldDB })
	apkStateDir, installedDBPath = dir, filepath.Join(dir, "installed")
	db := "P:nanokvm-rustdesk\nV:0.5.6-r0\n\nP:nanokvm-app\nV:3.6.0-r0\n\n"
	if err := os.WriteFile(installedDBPath, []byte(db), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveNotice(UpdateNotice{State: "ok", CheckedAt: 1, Updates: []SoftwareUpdate{
		{Name: "nanokvm-rustdesk", Installed: "0.5.5-r0", Available: "0.5.6-r0"},
		{Name: "nanokvm-app", Installed: "3.6.0-r0", Available: "3.6.1-r0"},
		{Name: "nanokvm-removed", Installed: "1.0-r0", Available: "1.1-r0"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := GetNotice().Updates; len(got) != 1 || got[0].Name != "nanokvm-app" {
		t.Fatalf("%+v", got)
	}
}

func TestRollbackWorkerUsesCompleteCoreSetAndFailsClosed(t *testing.T) {
	dir := communityFixture(t, communityOff)
	oldRecovery := rollbackRecoveryRoot
	rollbackRecoveryRoot = dir
	t.Cleanup(func() { rollbackRecoveryRoot = oldRecovery })
	oldDir, oldExec, oldDB, oldWorld := apkStateDir, apkExecutable, installedDBPath, worldPath
	t.Cleanup(func() { apkStateDir, apkExecutable, installedDBPath, worldPath = oldDir, oldExec, oldDB, oldWorld })
	apkStateDir, apkExecutable = dir, filepath.Join(dir, "apk")
	installedDBPath, worldPath = filepath.Join(dir, "installed"), filepath.Join(dir, "world")
	os.WriteFile(installedDBPath, []byte("P:nanokvm\nV:3.4.0_beta1-r0\n\n"), 0600)
	os.WriteFile(worldPath, []byte("nanokvm-release\naddon\n"), 0600)
	var packages []string
	for _, name := range corePackages {
		packages = append(packages, name+"-3.3.4-r0")
	}
	calls := filepath.Join(dir, "calls")
	install := func(output string, failSimulation bool) {
		t.Helper()
		script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\ncase \"$*\" in\nupdate) exit 0;;\n*search*) cat <<'PACKAGES'\n" + output + "\nPACKAGES\n;;\ndel*) exit 0;;\nadd*) "
		if failSimulation {
			script += "exit 9"
		} else {
			script += "case \"$*\" in *--simulate*) exit 0;; *) test -f '" + filepath.Join(dir, "etc/init.d/nanokvm-rollback-recovery") + "' && test -L '" + filepath.Join(dir, "etc/runlevels/boot/nanokvm-rollback-recovery") + "';; esac"
		}
		script += ";;\n*) exit 99;;\nesac\n"
		if err := os.WriteFile(apkExecutable, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		os.Remove(calls)
	}
	install(strings.Join(packages, "\n"), false)
	if err := RunAPK("rollback"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(calls)
	var targets []string
	for _, name := range corePackages {
		targets = append(targets, name+"=3.3.4-r0")
	}
	expected := "add --virtual " + rollbackConstraint + " " + strings.Join(targets, " ")
	if !strings.Contains(string(data), expected+" --simulate\n"+expected+"\n") {
		t.Fatal(string(data))
	}
	if s := GetAPKStatus(); s.State != "installed" {
		t.Fatal(s)
	}
	install(strings.Join(packages[1:], "\n"), false)
	if RunAPK("rollback") == nil {
		t.Fatal("incomplete stable set accepted")
	}
	data, _ = os.ReadFile(calls)
	if strings.Contains(string(data), "add --virtual") {
		t.Fatal("installed incomplete set")
	}
	install(strings.Join(packages, "\n"), true)
	if RunAPK("rollback") == nil {
		t.Fatal("dependency failure ignored")
	}
	data, _ = os.ReadFile(calls)
	if strings.Count(string(data), "add --virtual") != 1 {
		t.Fatal("installed after failed simulation")
	}
}

func TestPackageSelectionBoundsAndNamespace(t *testing.T) {
	old := worldPath
	worldPath = filepath.Join(t.TempDir(), "world")
	t.Cleanup(func() { worldPath = old })
	world := "nanokvm><Q1a=\nnanokvm-rustdesk@testing><Q1b=\nnanokvmish><Q1c=\ntailscale><Q1d=\n" +
		"exact=1.0-r0\nupper<2\nfuzzy~1.0\ntagged@testing<=2\nlower>=1.0\nnormal\n"
	if err := os.WriteFile(worldPath, []byte(world), 0600); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(nanokvmFilePins(), ","); got != "nanokvm,nanokvm-rustdesk@testing" {
		t.Fatalf("file selections: %s", got)
	}
	skip, bounded := upgradeSelections()
	if got := strings.Join(skip, ","); got != "nanokvmish,tailscale,exact,upper,fuzzy,tagged" {
		t.Fatalf("skipped: %s", got)
	}
	if got := strings.Join(bounded, ","); got != "exact,upper,fuzzy,tagged" {
		t.Fatalf("bounded: %s", got)
	}
	plan := "(1/5) Upgrading upper (1.0-r0 -> 1.1-r0)\n(2/5) Upgrading fuzzy (1.0-r0 -> 1.0-r1)\n" +
		"(3/5) Upgrading normal (1.0-r0 -> 2.0-r0)\n(4/5) Installing dependency (1.0-r0)\n(5/5) Purging removed (1.0-r0)\n"
	if got := boundedUpgrades(plan, bounded); got != "upper-1.0-r0 < 1.1-r0\nfuzzy-1.0-r0 < 1.0-r1" {
		t.Fatalf("bounded upgrades: %s", got)
	}
}

func TestPendingUpgradesRefreshesInstalledVersion(t *testing.T) {
	listing := "fixture-1.0-r0 < 2.0-r0\nremoved-1.0-r0 < 2.0-r0\nalready-1.0-r0 < 2.0-r0"
	got := pendingUpgrades(listing, map[string]string{"fixture": "1.1-r0", "already": "2.0-r0"})
	if got != "fixture-1.1-r0 < 2.0-r0" {
		t.Fatalf("pending: %s", got)
	}
}
