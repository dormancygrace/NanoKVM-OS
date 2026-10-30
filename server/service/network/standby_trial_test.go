package network

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"NanoKVM-Server/internal/networktrial"
	"NanoKVM-Server/standby"
)

// Exercise the actual shutdown entry point with the production network trial,
// using only the existing temporary-file fixture and inert Ethernet restarter.
func expectStandbyTrialRefusal(t *testing.T) {
	t.Helper()
	if err := standby.Requests.Begin(); err != nil {
		t.Fatal(err)
	}
	defer standby.Requests.Cancel()
	prepared, err := standby.Prepare(standby.Config{})
	if prepared != nil || !errors.Is(err, networktrial.ErrPending) {
		t.Fatalf("pending trial escaped shutdown preflight: prepared=%v error=%v", prepared, err)
	}
}

func waitTrialIdle(t *testing.T) {
	t.Helper()
	waitFor(t, "the trial to finish", func() bool {
		trialMu.Lock()
		defer trialMu.Unlock()
		return active == nil && networktrial.CheckIdle(trialFile) == nil
	})
}

func TestStandbyRefusesTrialUntilConfirmation(t *testing.T) {
	newTrialFixture(t)
	trialDuration = time.Hour
	info := setEthernetOnTrial(t, newStatic)
	waitApplied(t)
	expectStandbyTrialRefusal(t)
	if err := confirmTrial(info.Token); err != nil {
		t.Fatal(err)
	}
	waitTrialIdle(t)
}

func TestStandbyRefusesRunningNetworkRollback(t *testing.T) {
	for _, source := range []string{"timeout", "boot"} {
		t.Run(source, func(t *testing.T) {
			f := newTrialFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			restartEthernet = func() error {
				if f.restarted.Add(1) == 1 && source == "timeout" {
					return nil
				}
				close(entered)
				<-release
				return nil
			}
			t.Cleanup(func() { unblock(); waitTrialIdle(t) })
			if source == "timeout" {
				setEthernetOnTrial(t, newStatic)
				waitApplied(t)
			} else {
				previous := "192.168.7.7/24 none\n"
				record, err := json.Marshal(trialRecord{Service: "ethernet", Files: map[string]*string{ethernetConfigFile: &previous}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(trialFile, record, 0600); err != nil {
					t.Fatal(err)
				}
				RestorePendingTrial()
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("rollback did not reach the restarter")
			}
			expectStandbyTrialRefusal(t)
			unblock()
			waitTrialIdle(t)
		})
	}
}

func TestStandbyRefusesRollbackRetryAfterRecordRemoval(t *testing.T) {
	f := newTrialFixture(t)
	trialRetry = time.Hour
	broken := filepath.Join(f.dir, "fail-removal-sync")
	if err := os.WriteFile(broken, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// The first sync succeeds while the record exists. Its removal sync
	// fails, leaving a live retry with no file for a stat-only guard to find.
	script := "#!/bin/sh\n[ -e \"" + trialFile + "\" ] || [ ! -e \"" + broken + "\" ]\n"
	if err := os.WriteFile(filepath.Join(f.dir, "sync"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	setEthernetOnTrial(t, newStatic)
	waitApplied(t)
	waitFor(t, "the failed removal sync to schedule a retry", func() bool {
		trialMu.Lock()
		defer trialMu.Unlock()
		return active != nil && active.reverting && active.retry != nil
	})
	if _, err := os.Lstat(trialFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected an absent record after its removal sync failed: %v", err)
	}
	expectStandbyTrialRefusal(t)
	trialMu.Lock()
	pending := active
	pending.retry.Stop()
	trialMu.Unlock()
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	finishRevert(pending, "retry after sync recovery", true)
	waitTrialIdle(t)
	if got := f.read(t, ethernetConfigFile); got != "192.168.1.50/24 192.168.1.1\n" {
		t.Fatalf("previous settings not preserved: %q", got)
	}
	if got := f.restarted.Load(); got != 3 {
		t.Fatalf("restart count: %d", got)
	}
}

func TestStandbyWaitsForApplyThatOutlivesRollback(t *testing.T) {
	f := newTrialFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	restartEthernet = func() error {
		if f.restarted.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	}
	t.Cleanup(func() { unblock(); waitTrialIdle(t) })
	setEthernetOnTrial(t, newStatic)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("apply did not reach the restarter")
	}
	waitFor(t, "rollback to complete while apply is still running", func() bool {
		trialMu.Lock()
		defer trialMu.Unlock()
		return active == nil
	})
	if _, err := os.Lstat(trialFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback did not remove the record: %v", err)
	}
	expectStandbyTrialRefusal(t)
	unblock()
	waitTrialIdle(t)
	if got := f.restarted.Load(); got != 2 {
		t.Fatalf("restart count: %d", got)
	}
}
