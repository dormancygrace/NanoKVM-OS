package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/storage"
)

func TestCompositionTransaction(t *testing.T) {
	current := usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, network: true, disk: true}
	candidate := usbComposition{mode: hid.ModeNormal, network: true, disk: true, serial: true, audio: true}
	for _, failure := range []string{"", "install", "stop", "start", "verify", "rollback"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "S03usbdev")
			initial := map[string]usbFileSnapshot{}
			for _, name := range append(append([]string{}, usbCompositionFlags...), "S03usbdev") {
				snap := usbFileSnapshot{}
				switch name {
				case "usb.ncm":
					snap = usbFileSnapshot{true, 0600, []byte("keep network marker\n")}
				case "usb.disk0":
					snap = usbFileSnapshot{true, 0640, []byte("/data/images/rescue.iso\n")}
				case "S03usbdev":
					snap = usbFileSnapshot{true, 0755, []byte("original script\n")}
				}
				path := filepath.Join(dir, name)
				if err := writeUSBFile(path, snap); err != nil {
					t.Fatal(err)
				}
				initial[path] = snap
			}
			calls := []string{}
			injected := false
			fail := func(phase string) error {
				if !injected && (failure == phase || failure == "rollback" && phase == "verify") {
					injected = true
					return errors.New("injected " + phase)
				}
				return nil
			}
			store := usbCompositionStore{bootDir: dir, scriptPath: script,
				install: func(mode string) error {
					calls = append(calls, "install")
					if err := os.WriteFile(script, []byte("new script\n"), 0755); err != nil {
						return err
					}
					return fail("install")
				},
				run: func(action string) error { calls = append(calls, action); return fail(action) },
				verify: func(s usbComposition) error {
					calls = append(calls, "verify")
					if s == current && failure == "rollback" {
						return errors.New("rollback gadget unbound")
					}
					return fail("verify")
				},
			}
			err := store.apply(current, candidate)
			if failure == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(calls, []string{"install", "stop", "start", "verify"}) {
					t.Fatalf("not one rebind: %v", calls)
				}
				for _, name := range []string{"usb.ncm", "usb.disk0"} {
					got, err := snapshotUSBFile(filepath.Join(dir, name))
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, initial[filepath.Join(dir, name)]) {
						t.Fatalf("lost contents or permissions: %s", name)
					}
				}
				for _, name := range []string{"usb.acm", "usb.audio", "disable_hid", "usb.disable_keyboard", "usb.disable_relative", "usb.disable_absolute"} {
					if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
						t.Fatalf("missing %s: %v", name, err)
					}
				}
				if _, err := os.Stat(filepath.Join(dir, "usb.rndis0")); !os.IsNotExist(err) {
					t.Fatal("NCM silently replaced with RNDIS")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("lost cause: %v", err)
			}
			if failure == "rollback" && !strings.Contains(err.Error(), "restoring USB configuration also failed") {
				t.Fatalf("lost rollback error: %v", err)
			}
			for path, want := range initial {
				got, err := snapshotUSBFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("rollback differs for %s: got %#v want %#v", path, got, want)
				}
			}
		})
	}
}

func TestRemoteAccessUSBDefaultsPreserveOtherFunctions(t *testing.T) {
	current := usbComposition{mode: hid.ModeNormal, network: true, disk: true}
	candidate := current.remoteAccessDefaults(true)
	if !candidate.keyboard || !candidate.relative || !candidate.absolute || !candidate.audio ||
		!candidate.network || !candidate.disk {
		t.Fatalf("required or existing functions lost: %+v", candidate)
	}
	if err := candidate.validate(); err != nil {
		t.Fatal(err)
	}
	if candidate.remoteAccessDefaults(true) != candidate {
		t.Fatal("defaults are not idempotent")
	}
	withoutSound := current.remoteAccessDefaults(false)
	if withoutSound.audio {
		t.Fatal("disabled RustDesk sound enabled USB audio")
	}
	if candidate.remoteAccessDefaults(false).audio != candidate.audio {
		t.Fatal("disabling RustDesk sound removed shared USB audio")
	}
	hidOnly := (usbComposition{mode: hid.ModeHidOnly}).remoteAccessDefaults(true)
	if hidOnly.mode != hid.ModeNormal || !hidOnly.audio {
		t.Fatal("sound cannot be enabled in HID-only mode")
	}
	full := usbComposition{mode: hid.ModeNormal, network: true, disk: true, serial: true}
	if full.remoteAccessDefaults(true).validate() == nil {
		t.Fatal("defaults exceeded endpoint budget silently")
	}
}

func TestCompositionNoOpAndInvalidDraftHaveNoSideEffects(t *testing.T) {
	current := usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, network: true, disk: true}
	// Missing callbacks and paths deliberately make any attempted mutation fail.
	store := usbCompositionStore{}
	if err := store.apply(current, current); err != nil {
		t.Fatal(err)
	}
	tooLarge := current
	tooLarge.serial = true
	for _, candidate := range []usbComposition{tooLarge, {mode: "invalid", keyboard: true}, {mode: hid.ModeHidOnly, network: true}} {
		if err := store.apply(current, candidate); err == nil {
			t.Fatalf("accepted invalid draft: %+v", candidate)
		}
	}
}

func TestNetworkProtocolSelection(t *testing.T) {
	for _, tc := range []struct {
		name                string
		rndis, ncm, wantNCM bool
	}{
		{"new network prefers NCM", false, false, true},
		{"preserve selected NCM", false, true, true},
		{"migrate selected RNDIS to NCM", true, false, true},
		{"both markers select only NCM", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, exists := range map[string]bool{"usb.ncm": tc.ncm, "usb.rndis0": tc.rndis} {
				if exists {
					if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			store := usbCompositionStore{bootDir: dir, scriptPath: filepath.Join(dir, "S03usbdev"),
				install: func(string) error { return nil }, run: func(string) error { return nil },
				verify: func(usbComposition) error { return nil },
			}
			current := usbComposition{mode: hid.ModeNormal, keyboard: true}
			candidate := current
			candidate.network = true
			if err := store.apply(current, candidate); err != nil {
				t.Fatal(err)
			}
			_, ncmErr := os.Stat(filepath.Join(dir, "usb.ncm"))
			_, rndisErr := os.Stat(filepath.Join(dir, "usb.rndis0"))
			if (ncmErr == nil) != tc.wantNCM || (rndisErr == nil) == tc.wantNCM {
				t.Fatalf("unexpected protocol selection: ncm=%v rndis=%v", ncmErr, rndisErr)
			}
		})
	}
}

func TestAudioBudgetAndRevision(t *testing.T) {
	base := usbComposition{mode: hid.ModeNormal, disk: true, serial: true}
	with := base.with("audio", true)
	in, out := with.endpointUsage()
	if in != 3 || out != 3 || with.validate() != nil {
		t.Fatalf("USB audio budget: %d/%d", in, out)
	}
	if with.revision() == base.revision() {
		t.Fatal("audio absent from revision")
	}
	with.mode = hid.ModeHidOnly
	if with.validate() == nil {
		t.Fatal("audio allowed in compatibility mode")
	}
}

// The identity files carry a value, are written only when they differ from
// the factory one, and go away when the identity returns to the default; a
// failed apply puts the previous ones back.
func TestCompositionWritesAndRestoresIdentityFiles(t *testing.T) {
	dir := t.TempDir()
	noop := usbCompositionStore{bootDir: dir, scriptPath: filepath.Join(dir, "S03usbdev"),
		install: func(string) error { return nil },
		run:     func(string) error { return nil },
		verify:  func(usbComposition) error { return nil },
	}
	base := usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, disk: true}
	custom := base
	custom.identity = usbIdentity{VendorID: "0X46D", Manufacturer: "Generic", Product: "USB Composite Device", SerialNumber: "KVM-0001"}
	if err := noop.apply(base, custom); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "<absent>"
		}
		return string(data)
	}
	if read("usb.vid") != "0x046d\n" || read("usb.pid") != "<absent>" || read("usb.manufacturer") != "Generic\n" || read("usb.product") != "USB Composite Device\n" || read("usb.serialnumber") != "KVM-0001\n" {
		t.Fatalf("identity files: %q %q %q %q", read("usb.vid"), read("usb.pid"), read("usb.manufacturer"), read("usb.product"))
	}
	// the flags of the composition itself are untouched
	if _, err := os.Stat(filepath.Join(dir, "usb.disk0")); err != nil {
		t.Fatal("the disk flag went")
	}

	// a failed apply back to the default keeps the custom files
	failing := noop
	failing.verify = func(usbComposition) error { return errors.New("injected verify") }
	if err := failing.apply(custom, base); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("err %v", err)
	}
	if read("usb.vid") != "0x046d\n" || read("usb.product") != "USB Composite Device\n" {
		t.Fatal("rollback lost the identity files")
	}

	// back to the default removes them
	if err := noop.apply(custom, base); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"usb.vid", "usb.pid", "usb.manufacturer", "usb.product", "usb.serialnumber"} {
		if read(name) != "<absent>" {
			t.Fatalf("%s is still there", name)
		}
	}
	if base.revision() == custom.revision() {
		t.Fatal("the identity is not part of the revision")
	}
	if (usbComposition{mode: hid.ModeNormal, keyboard: true, identity: usbIdentity{VendorID: "nope"}}).validate() == nil {
		t.Fatal("a bad identity validated")
	}
}

// The user disk marker names the image while the disk is on, goes away when
// it is off, and needs the virtual disk; verification wants the third LUN
// to match.
func TestCompositionUserDiskMarkerAndLun(t *testing.T) {
	dir := t.TempDir()
	noop := usbCompositionStore{bootDir: dir, scriptPath: filepath.Join(dir, "S03usbdev"),
		install: func(string) error { return nil },
		run:     func(string) error { return nil },
		verify:  func(usbComposition) error { return nil },
	}
	base := usbComposition{mode: hid.ModeNormal, keyboard: true, disk: true}
	withDisk := base
	withDisk.userDisk = true
	if err := noop.apply(base, withDisk); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "usb.userdisk"))
	if err != nil || strings.TrimSpace(string(data)) != storage.UserDiskImagePath() {
		t.Fatalf("marker %q %v", data, err)
	}
	if !userDiskFlagged(dir) {
		t.Fatal("the marker is not read back")
	}
	if err := noop.apply(withDisk, base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "usb.userdisk")); !os.IsNotExist(err) {
		t.Fatal("the marker stayed")
	}
	if (usbComposition{mode: hid.ModeNormal, keyboard: true, userDisk: true}).validate() == nil {
		t.Fatal("a user disk without the virtual disk validated")
	}
	if base.revision() == withDisk.revision() {
		t.Fatal("the user disk is not in the revision")
	}

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "configs/c.1"), 0755)
	os.MkdirAll(filepath.Join(root, "functions/mass_storage.disk0"), 0755)
	os.WriteFile(filepath.Join(root, "UDC"), []byte("4340000.usb\n"), 0644)
	os.Symlink("../../functions/hid.GS0", filepath.Join(root, "configs/c.1/hid.GS0"))
	os.Symlink("../../functions/mass_storage.disk0", filepath.Join(root, "configs/c.1/mass_storage.disk0"))
	writeGadgetIdentity(t, root, defaultUSBIdentity)
	if err := verifyUSBCompositionAt(root, base); err != nil {
		t.Fatal(err)
	}
	if err := verifyUSBCompositionAt(root, withDisk); err == nil {
		t.Fatal("no lun.2, yet the user disk verified")
	}
	os.MkdirAll(filepath.Join(root, "functions/mass_storage.disk0/lun.2"), 0755)
	if err := verifyUSBCompositionAt(root, withDisk); err != nil {
		t.Fatal(err)
	}
	if err := verifyUSBCompositionAt(root, base); err == nil {
		t.Fatal("lun.2 present, yet no user disk verified")
	}
}

func TestEmptyCompositionRequiresUnboundController(t *testing.T) {
	root := t.TempDir()
	empty := usbComposition{mode: hid.ModeNormal}
	if err := empty.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "UDC"), []byte("\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyUSBCompositionAt(root, empty); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "UDC"), []byte("4340000.usb"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyUSBCompositionAt(root, empty); err == nil {
		t.Fatal("empty gadget remained attached")
	}
}

func TestCompositionPreservesAlpineScriptLink(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprint(rollback), func(t *testing.T) {
			d := t.TempDir()
			target := filepath.Join(d, "legacy")
			link := filepath.Join(d, "S03usbdev")
			if err := os.WriteFile(target, []byte("original"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("legacy", link); err != nil {
				t.Fatal(err)
			}
			current := usbComposition{mode: hid.ModeNormal, keyboard: true}
			candidate := usbComposition{mode: hid.ModeNormal}
			store := usbCompositionStore{bootDir: d, scriptPath: link,
				install: func(string) error { return os.WriteFile(target, []byte("new"), 0755) },
				run:     func(string) error { return nil },
				verify: func(s usbComposition) error {
					if rollback && s == candidate {
						return errors.New("injected verify failure")
					}
					return nil
				},
			}
			err := store.apply(current, candidate)
			if (err != nil) != rollback {
				t.Fatalf("unexpected result: %v", err)
			}
			if got, err := os.Readlink(link); err != nil || got != "legacy" {
				t.Fatalf("link changed: %q %v", got, err)
			}
			want := "new"
			if rollback {
				want = "original"
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != want {
				t.Fatalf("wrong script: %q %v", got, err)
			}
		})
	}
}

func TestApplyRefusesTheUserDiskWhileTheImageIsBeingMade(t *testing.T) {
	unlock, err := storage.LockUserDiskForComposition()
	if err != nil {
		t.Fatal(err)
	}
	// Stands in for a create or delete holding the lock.
	defer unlock()
	current := usbComposition{mode: hid.ModeNormal, keyboard: true, disk: true}
	for _, candidate := range []usbComposition{
		{mode: hid.ModeNormal, keyboard: true, disk: true, userDisk: true},
	} {
		if err := applyLiveUSBComposition(nil, current, candidate); !errors.Is(err, storage.ErrUserDiskBusy) {
			t.Fatalf("attach during a format: %v", err)
		}
		if err := applyLiveUSBComposition(nil, candidate, current); !errors.Is(err, storage.ErrUserDiskBusy) {
			t.Fatalf("detach during a format: %v", err)
		}
	}
}

// Printable USB strings may contain colons. Revision serialization must
// distinguish moving a colon-delimited segment between the two strings.
func TestUSBRevisionDistinguishesIdentityFieldBoundaries(t *testing.T) {
	first := usbComposition{mode: hid.ModeNormal, keyboard: true,
		identity: usbIdentity{Manufacturer: "Acme:Console", Product: "Bridge"}}
	second := first
	second.identity.Manufacturer = "Acme"
	second.identity.Product = "Console:Bridge"
	if err := first.validate(); err != nil {
		t.Fatal(err)
	}
	if err := second.validate(); err != nil {
		t.Fatal(err)
	}
	if first.revision() == second.revision() {
		t.Fatal("different valid identities share a revision; stale clients can overwrite a concurrent identity change")
	}
}
