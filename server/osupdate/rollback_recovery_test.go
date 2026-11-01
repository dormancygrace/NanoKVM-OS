package osupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoticeRecoveryNeverRemovesActiveRollback(t *testing.T) {
	dir := t.TempDir()
	oldDB, oldExec, oldRoot, oldLock := installedDBPath, apkExecutable, rollbackRecoveryRoot, updateLock
	t.Cleanup(func() {
		installedDBPath, apkExecutable, rollbackRecoveryRoot, updateLock = oldDB, oldExec, oldRoot, oldLock
	})
	installedDBPath, apkExecutable, rollbackRecoveryRoot = filepath.Join(dir, "installed"), filepath.Join(dir, "apk"), dir
	os.WriteFile(installedDBPath, []byte("P:"+rollbackConstraint+"\nV:1\n\n"), 0600)
	calls := filepath.Join(dir, "calls")
	os.WriteFile(apkExecutable, []byte("#!/bin/sh\necho \"$*\" >> '"+calls+"'\nexit 0\n"), 0700)
	if err := installRollbackRecovery(); err != nil {
		t.Fatal(err)
	}
	updateLock = func() (*os.File, error) { return nil, errors.New("update active") }
	if recoverInterruptedRollback(context.Background()) == nil {
		t.Fatal("ignored update lock")
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatal("ran apk during rollback")
	}
	script, _ := recoveryPaths()
	updateLock = func() (*os.File, error) { return os.Open(dir) }
	os.WriteFile(apkExecutable, []byte("#!/bin/sh\nexit 9\n"), 0700)
	if recoverInterruptedRollback(context.Background()) == nil {
		t.Fatal("ignored failed cleanup")
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatal("removed boot fallback after error")
	}
	os.WriteFile(apkExecutable, []byte("#!/bin/sh\necho \"$*\" >> '"+calls+"'\nexit 0\n"), 0700)
	if err := recoverInterruptedRollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(calls)
	if !strings.Contains(string(data), "--no-network del "+rollbackConstraint) {
		t.Fatal(string(data))
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatal("did not remove successful recovery")
	}
}
