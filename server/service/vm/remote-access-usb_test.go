package vm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"NanoKVM-Server/service/hid"
)

func TestRustDeskPresetAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usb-before-rustdesk.json")
	identity := usbIdentity{Product: "USB Composite Device"}
	headless := usbComposition{mode: hid.ModeNormal, disk: true, serial: true, identity: identity}
	var applied []usbComposition
	apply := func(c usbComposition) error { applied = append(applied, c); return nil }

	if err := enableRemoteAccessUSB(path, headless, true, apply); err != nil {
		t.Fatal(err)
	}
	preset := usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, disk: true, audio: true, identity: identity}
	if len(applied) != 1 || applied[0] != preset {
		t.Fatalf("applied %+v", applied)
	}
	// turning off restores what was on, with the identity in place then
	identity2 := usbIdentity{Product: "Other"}
	now := preset
	now.identity = identity2
	if err := restoreRemoteAccessUSB(path, now, apply); err != nil {
		t.Fatal(err)
	}
	want := headless
	want.identity = identity2
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

func TestRustDeskSoundAndFailedRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usb-before-rustdesk.json")
	off := usbComposition{mode: hid.ModeNormal}
	ok := func(usbComposition) error { return nil }
	if err := enableRemoteAccessUSB(path, off, false, ok); err != nil {
		t.Fatal(err)
	}
	preset := off.remoteAccessProfile(false)
	// RustDesk's sound turned on while it ran: still restored afterwards
	withSound := preset.remoteAccessDefaults(true)
	if err := followRemoteAccessMarker(path, preset, withSound); err != nil {
		t.Fatal(err)
	}
	// a failed restore keeps the marker; on and off again retries
	if err := restoreRemoteAccessUSB(path, withSound, func(usbComposition) error { return errors.New("injected") }); err == nil {
		t.Fatal("the failure was lost")
	}
	if err := enableRemoteAccessUSB(path, withSound, true, ok); err != nil {
		t.Fatal(err)
	}
	var restored usbComposition
	if err := restoreRemoteAccessUSB(path, off.remoteAccessProfile(true), func(c usbComposition) error { restored = c; return nil }); err != nil {
		t.Fatal(err)
	}
	// an empty composition before RustDesk turns USB off again
	if restored != off {
		t.Fatalf("restored %+v, want USB off", restored)
	}
}
