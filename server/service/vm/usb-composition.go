package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/storage"
	"NanoKVM-Server/service/stream/audio"
)

func (s usbComposition) validate() error {
	if s.mode != hid.ModeNormal && s.mode != hid.ModeHidOnly {
		return errors.New("invalid USB mode")
	}
	if s.mode == hid.ModeHidOnly && (s.network || s.disk || s.audio) {
		return errors.New("USB network, disk and audio are unavailable in HID-only mode")
	}
	if !s.fitsEndpointBudget() {
		return errors.New("USB endpoint budget exceeded")
	}
	if s.userDisk && !s.disk {
		return errors.New("the user disk needs the virtual disk")
	}
	return s.identity.validate()
}

// EnsureRemoteAccessUSB applies the same validated, serialized composition as
// the USB settings screen. Other enabled functions survive.
func EnsureRemoteAccessUSB(withAudio bool) error {
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	current := getUSBComposition()
	candidate := current.remoteAccessDefaults(withAudio)
	if err := candidate.validate(); err != nil {
		return err
	}
	if current == candidate {
		return verifyLiveUSBComposition(candidate)
	}
	return applyLiveUSBComposition(h, current, candidate)
}

func (s usbComposition) remoteAccessDefaults(withAudio bool) usbComposition {
	s.keyboard, s.relative, s.absolute = true, true, true
	if withAudio {
		s.audio, s.mode = true, hid.ModeNormal
	}
	return s
}

// errUserDiskMissing: the image was deleted between the request and the apply.
var errUserDiskMissing = errors.New("create the user disk first")

func applyLiveUSBComposition(h *hid.Hid, current, candidate usbComposition) error {
	if current == candidate {
		return nil
	}
	// Attaching or detaching the user disk excludes creating or deleting
	// the image for the whole apply (storage.LockUserDiskForComposition).
	if current.userDisk || candidate.userDisk {
		unlock, err := storage.LockUserDiskForComposition()
		if err != nil {
			return err
		}
		defer unlock()
		if candidate.userDisk && !storage.UserDiskExists() {
			return errUserDiskMissing
		}
	}
	if err := audio.Stop(); err != nil {
		return err
	}
	if candidate.serial && !current.serial {
		if err := retireLegacyACMGetty(inittabPath); err != nil {
			return err
		}
	}
	store := usbCompositionStore{
		bootDir: "/boot", scriptPath: hid.USBDevScript,
		install: hid.InstallModeScript,
		run: func(action string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, "sh", hid.USBDevScript, action).CombinedOutput()
			if err != nil {
				return fmt.Errorf("USB %s: %w: %s", action, err, output)
			}
			return nil
		},
		verify: verifyLiveUSBComposition,
	}
	h.CloseNoLock()
	defer h.OpenNoLock()
	return store.apply(current, candidate)
}

func (s usbComposition) empty() bool {
	return !s.keyboard && !s.relative && !s.absolute && !s.network && !s.disk && !s.serial && !s.audio
}
func verifyLiveUSBComposition(s usbComposition) error {
	return verifyUSBCompositionAt("/sys/kernel/config/usb_gadget/g0", s)
}
func verifyUSBCompositionAt(root string, s usbComposition) error {
	udc, err := os.ReadFile(filepath.Join(root, "UDC"))
	if err != nil {
		return err
	}
	bound := strings.TrimSpace(string(udc)) != ""
	if bound == s.empty() {
		return errors.New("USB controller binding does not match the composition")
	}
	linked := func(name string) bool {
		info, err := os.Lstat(filepath.Join(root, "configs/c.1", name))
		return err == nil && info.Mode()&os.ModeSymlink != 0
	}
	if linked("hid.GS0") != s.keyboard || linked("hid.GS1") != s.relative ||
		linked("hid.GS2") != s.absolute || linked("mass_storage.disk0") != s.disk ||
		linked("acm.GS0") != s.serial || linked("uac1.audio0") != s.audio || (linked("rndis.usb0") || linked("ncm.usb0")) != s.network {
		return errors.New("USB functions do not match the requested composition")
	}
	if s.disk {
		// the user disk is the third LUN of the storage function
		_, err := os.Stat(filepath.Join(root, "functions/mass_storage.disk0/lun.2"))
		if (err == nil) != s.userDisk {
			return errors.New("the user disk LUN does not match the requested composition")
		}
	}
	if bound {
		presented, err := gadgetIdentityAt(root)
		if err != nil {
			return fmt.Errorf("read the USB identity: %w", err)
		}
		if presented != s.identity.normalized() {
			return errors.New("USB identity does not match the requested one")
		}
	}
	return nil
}

// The boot files one apply may change, snapshotted and restored together.
// The first ten are flags (present or absent); the identity files and the
// user disk marker carry a value and are absent when not in use.
var usbCompositionFlags = []string{
	"usb.rndis0", "usb.ncm", "usb.disk0", "usb.acm", "usb.audio", "disable_hid",
	"usb.disable_keyboard", "usb.disable_relative", "usb.disable_absolute",
	"usb.vid", "usb.pid", "usb.manufacturer", "usb.product", "usb.userdisk",
}

// contentFiles is what the value-bearing boot files should hold for a
// composition: the identity files, and the user disk marker naming the image.
func (s usbComposition) contentFiles() map[string]*string {
	files := s.identity.files()
	files["usb.userdisk"] = nil
	if s.userDisk {
		image := storage.UserDiskImagePath()
		files["usb.userdisk"] = &image
	}
	return files
}

type usbFileSnapshot struct {
	exists bool
	mode   os.FileMode
	data   []byte
}

type usbCompositionStore struct {
	bootDir, scriptPath string
	install             func(mode string) error
	run                 func(action string) error
	verify              func(usbComposition) error
}

func snapshotUSBFile(path string) (usbFileSnapshot, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return usbFileSnapshot{}, nil
	}
	if err != nil {
		return usbFileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return usbFileSnapshot{}, fmt.Errorf("USB configuration is not a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	return usbFileSnapshot{exists: true, mode: info.Mode().Perm(), data: data}, err
}

func writeUSBFile(path string, snapshot usbFileSnapshot) error {
	if !snapshot.exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".usb-composition-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(snapshot.mode); err != nil {
		return err
	}
	if _, err := f.Write(snapshot.data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Apply a whole draft with one stop/start. Runtime failures restore the exact
// previous flag contents (including mounted-image paths) and installed script.
// Multiple boot files are not a power-loss-atomic database transaction.
func (store usbCompositionStore) apply(current, candidate usbComposition) error {
	if err := candidate.validate(); err != nil {
		return err
	}
	if current == candidate {
		return nil
	}

	// Alpine exposes the legacy script through a compatibility symlink. Snapshot
	// and restore the actual file, leaving the packaged link intact.
	if info, err := os.Lstat(store.scriptPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		store.scriptPath, err = filepath.EvalSymlinks(store.scriptPath)
		if err != nil {
			return fmt.Errorf("resolve USB init script: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	paths := make([]string, 0, len(usbCompositionFlags)+1)
	for _, name := range usbCompositionFlags {
		paths = append(paths, filepath.Join(store.bootDir, name))
	}
	paths = append(paths, store.scriptPath)
	before := make(map[string]usbFileSnapshot, len(paths))
	for _, path := range paths {
		snapshot, err := snapshotUSBFile(path)
		if err != nil {
			return err
		}
		before[path] = snapshot
	}
	stopped := false
	rollback := func(cause error) error {
		var errs []error
		if stopped {
			errs = append(errs, store.run("stop"))
		}
		for _, path := range paths {
			errs = append(errs, writeUSBFile(path, before[path]))
		}
		if stopped {
			if err := store.run("start"); err != nil {
				errs = append(errs, err)
			} else {
				errs = append(errs, store.verify(current))
			}
		}
		if err := errors.Join(errs...); err != nil {
			return fmt.Errorf("%w; restoring USB configuration also failed: %v", cause, err)
		}
		return cause
	}
	if err := store.install(candidate.mode); err != nil {
		return rollback(err)
	}
	stopped = true
	if err := store.run("stop"); err != nil {
		return rollback(err)
	}

	// USB networking uses NCM; remove any legacy RNDIS preference on apply.
	flags := map[string]bool{
		"usb.rndis0": false, "usb.ncm": candidate.network,
		"usb.disk0": candidate.disk, "usb.acm": candidate.serial, "usb.audio": candidate.audio,
		"disable_hid":          !candidate.keyboard && !candidate.relative && !candidate.absolute,
		"usb.disable_keyboard": !candidate.keyboard,
		"usb.disable_relative": !candidate.relative,
		"usb.disable_absolute": !candidate.absolute,
	}
	contents := candidate.contentFiles()
	for _, name := range usbCompositionFlags {
		path := filepath.Join(store.bootDir, name)
		if value, hasContent := contents[name]; hasContent {
			// a value, compared by content; absent when not in use
			want := usbFileSnapshot{}
			if value != nil {
				want = usbFileSnapshot{exists: true, mode: 0644, data: []byte(*value + "\n")}
			}
			if want.exists == before[path].exists && (!want.exists || string(want.data) == string(before[path].data)) {
				continue
			}
			if err := writeUSBFile(path, want); err != nil {
				return rollback(err)
			}
			continue
		}
		// a flag, by presence; its content (an image path, say) is kept
		if flags[name] == before[path].exists {
			continue
		}
		if err := writeUSBFile(path, usbFileSnapshot{exists: flags[name], mode: 0644}); err != nil {
			return rollback(err)
		}
	}
	if err := store.run("start"); err != nil {
		return rollback(err)
	}
	if err := store.verify(candidate); err != nil {
		return rollback(err)
	}
	return nil
}
