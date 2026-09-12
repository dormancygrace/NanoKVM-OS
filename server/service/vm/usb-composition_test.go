package vm

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

func TestEmptyCompositionRequiresUnboundController(t *testing.T) {
    root:=t.TempDir()
    empty:=usbComposition{mode:hid.ModeNormal}
    if err:=empty.validate();err!=nil {t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(root,"UDC"),[]byte("\n"),0644);err!=nil{t.Fatal(err)}
    if err:=verifyUSBCompositionAt(root,empty);err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(root,"UDC"),[]byte("4340000.usb"),0644);err!=nil{t.Fatal(err)}
    if err:=verifyUSBCompositionAt(root,empty);err==nil{t.Fatal("empty gadget remained attached")}
}
