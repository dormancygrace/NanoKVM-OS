package network

import (
	"encoding/json"
	"errors"
	"os"
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
