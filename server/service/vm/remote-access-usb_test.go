package vm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"NanoKVM-Server/internal/storageidentity"
	"NanoKVM-Server/service/hid"
)

func TestRustDeskPresetAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usb-before-rustdesk.json")
	identity := usbIdentity{Product: "USB Composite Device"}
	headless := usbComposition{mode: hid.ModeNormal, disk: true, serial: true, identity: identity, storageIdentity: storageidentity.Identity{Vendor: "Generic", Image: "Rescue"}}
	var applied []usbComposition
	apply := func(c usbComposition) error { applied = append(applied, c); return nil }

	if err := enableRemoteAccessUSB(path, headless, true, apply); err != nil {
		t.Fatal(err)
	}
	preset := usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, disk: true, audio: true, identity: identity, storageIdentity: storageidentity.Identity{Vendor: "Generic", Image: "Rescue"}}
	if len(applied) != 1 || applied[0] != preset {
		t.Fatalf("applied %+v", applied)
	}
	// turning off restores what was on, with the identity in place then
	identity2 := usbIdentity{Product: "Other"}
	now := preset
	now.identity = identity2
	now.storageIdentity.Image = "New name"
	if err := restoreRemoteAccessUSB(path, now, apply); err != nil {
		t.Fatal(err)
	}
	want := headless
	want.identity = identity2
	want.storageIdentity = now.storageIdentity
	if len(applied) != 2 || applied[1] != want {
		t.Fatalf("restored %+v", applied)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the marker stayed")
	}

	// no marker (RustDesk on before this version): nothing to restore
	if err := restoreRemoteAccessUSB(path, preset, apply); err != nil || len(applied) != 2 {
		t.Fatalf("restored without a marker: %v %d", err, len(applied))
	}
}

func TestRustDeskRestoreKeepsAChangedComposition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usb-before-rustdesk.json")
	kvm := usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, disk: true}
	var applied []usbComposition
	apply := func(c usbComposition) error { applied = append(applied, c); return nil }
	if err := enableRemoteAccessUSB(path, kvm, false, apply); err != nil {
		t.Fatal(err)
	}
	// the user turned network on while RustDesk ran
	changed := kvm
	changed.network = true
	if err := restoreRemoteAccessUSB(path, changed, apply); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Fatalf("a changed composition was replaced: %+v", applied)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the marker stayed")
	}
}

func TestRustDeskMarkerMustBeSavedBeforeSwitchingUSB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "marker.json")
	current := usbComposition{mode: hid.ModeNormal, network: true}
	called := false
	err := enableRemoteAccessUSB(path, current, true, func(usbComposition) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("marker failure changed USB: called=%t err=%v", called, err)
	}
}

func TestRustDeskMarkerPrecedesApplyAndRollsBackOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker.json")
	current := usbComposition{mode: hid.ModeNormal, serial: true}
	failed := errors.New("apply failed")
	err := enableRemoteAccessUSB(path, current, true, func(candidate usbComposition) error {
		marker, ok := readRemoteAccessMarker(path)
		if !ok || marker.Before != flagsOf(current) || marker.Applied != flagsOf(candidate) {
			t.Error("USB changed before its recovery data was saved")
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed apply left a marker for an unapplied preset")
	}
}
