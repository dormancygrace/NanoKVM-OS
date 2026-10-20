package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeAPKWorker(t *testing.T) {
	oldDir, oldExec := apkStateDir, apkExecutable
	defer func() { apkStateDir, apkExecutable = oldDir, oldExec }()
	apkStateDir = t.TempDir()
	apkExecutable = filepath.Join(t.TempDir(), "apk")
	install := func(script string) {
		t.Helper()
		if e := os.WriteFile(apkExecutable, []byte("#!/bin/sh\n"+script), 0700); e != nil {
			t.Fatal(e)
		}
	}
	install(`case "$1" in update) exit 0;; version) printf 'Installed: Available:\nnanokvm-kernel-sg2002-2.0_alpha1-r1 < 2.0_alpha2-r0\n';; upgrade) exit 0;; *) exit 99;; esac
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
