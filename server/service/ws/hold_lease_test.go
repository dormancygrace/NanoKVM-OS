package ws

import (
	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/inputcontrol"
	"context"
	"path/filepath"
	"testing"
)

func TestHeldWatchOptInIdleAndMixedInput(t *testing.T) {
	c := &Client{}
	defer c.stopHeldInputWatch()
	c.inputMutex.Lock()
	c.noteHeldInputLocked(inputcontrol.ManualKeyboard, true)
	if c.hold.timer != nil {
		t.Fatal("legacy client acquired a timer")
	}
	c.inputMutex.Unlock()
	c.keepHeldInputAlive()
	c.inputMutex.Lock()
	if c.hold.timer == nil {
		t.Fatal("opt-in did not protect an already held key")
	}
	c.noteHeldInputLocked(inputcontrol.ManualRelativeMouse, true)
	c.noteHeldInputLocked(inputcontrol.ManualKeyboard, false)
	if c.hold.timer == nil {
		t.Fatal("key-up removed the held mouse timer")
	}
	c.noteHeldInputLocked(inputcontrol.ManualRelativeMouse, false)
	if c.hold.timer != nil {
		t.Fatal("idle client retains a timer")
	}
	c.inputMutex.Unlock()
}

func TestHeldExpiryRetiresQueueButPreservesNewOwner(t *testing.T) {
	for _, owner := range []bool{true, false} {
		t.Run(map[bool]string{true: "owner", false: "transferred"}[owner], func(t *testing.T) {
			control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP)
			manual := inputcontrol.NewManualSession(control, &inputcontrol.Coordinator{})
			defer manual.Close()
			queued, err := manual.Reserve(context.Background(), inputcontrol.ManualKeyboard, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			c := &Client{manual: manual}
			defer c.stopHeldInputWatch()
			c.hold.held[0] = true
			c.hold.generation = 2
			manager := newManager()
			if owner {
				manager.controller = c
			} else {
				manager.controller = &Client{}
				manual.Revoke()
			}
			releases := 0
			release := func() error { releases++; return nil }
			c.expireHeldInput(manager, 1, release)
			if releases != 0 || !c.hold.held[0] {
				t.Fatal("stale expiry affected current input")
			}
			c.expireHeldInput(manager, 2, release)
			want := 0
			if owner {
				want = 1
			}
			if releases != want {
				t.Fatalf("releases %d want %d", releases, want)
			}
			if c.hold.held != [3]bool{} {
				t.Fatal("expired state retained")
			}
			wrote := false
			if err := queued.Execute(func() error { wrote = true; return nil }); err == nil || wrote {
				t.Fatal("old queued input survived expiry/transfer")
			}
			queued.Complete(false)
			if owner && !manager.CanControl(c) {
				t.Fatal("expiry closed input ownership")
			}
		})
	}
}
