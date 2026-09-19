package osupdate

import (
	"os"
	"path/filepath"
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
