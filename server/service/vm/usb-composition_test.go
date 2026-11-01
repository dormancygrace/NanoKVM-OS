package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"NanoKVM-Server/internal/storageidentity"
	"NanoKVM-Server/service/hid"
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

func writeStorageLUN(t *testing.T, root string, n int, kind string, identity storageidentity.Identity) {
	t.Helper()
	dir := filepath.Join(root, "functions/mass_storage.disk0", fmt.Sprintf("lun.%d", n))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	cdrom := "0"
	if kind == "cdrom" {
		cdrom = "1"
	}
	for name, value := range map[string]string{"cdrom": cdrom, "file": "", "inquiry_string": identity.Inquiry(kind)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
func TestStorageNamesRevisionProfileAndVerification(t *testing.T) {
	base := usbComposition{mode: hid.ModeNormal, disk: true}
	custom := base
	custom.storageIdentity = storageidentity.Identity{Vendor: "Generic", Image: "Rescue", Setup: "Tools", User: "Files"}
	if base.revision() == custom.revision() {
		t.Fatal("names missing from revision")
	}
	if custom.remoteAccessProfile(true).storageIdentity != custom.storageIdentity {
		t.Fatal("RustDesk lost names")
	}
	root := t.TempDir()
	for n, kind := range []string{"cdrom", "setup", "user"} {
		writeStorageLUN(t, root, n, kind, custom.storageIdentity)
	}
	if err := verifyStorageIdentityAt(root, custom.storageIdentity); err != nil {
		t.Fatal(err)
	}
	if err := verifyStorageIdentityAt(root, storageidentity.Defaults); err == nil {
		t.Fatal("wrong names accepted")
	}
	custom.storageIdentity.Image = "too long for SCSI name"
	if custom.validate() == nil {
		t.Fatal("invalid name accepted")
	}
}
