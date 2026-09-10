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
)

func (s usbComposition) validate() error {
	if s.mode != hid.ModeNormal && s.mode != hid.ModeHidOnly {
		return errors.New("invalid USB mode")
	}
	if s.mode == hid.ModeHidOnly && (s.network || s.disk) {
		return errors.New("USB network and disk are unavailable in HID-only mode")
	}
	if !s.fitsEndpointBudget() {
		return errors.New("USB endpoint budget exceeded")
	}
	if !s.keyboard && !s.relative && !s.absolute && !s.network && !s.disk && !s.serial {
		return errors.New("select at least one USB function")
	}
	return nil
}

func applyLiveUSBComposition(h *hid.Hid, current, candidate usbComposition) error {
	if current == candidate {
		return nil
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

func verifyLiveUSBComposition(s usbComposition) error {
	root := "/sys/kernel/config/usb_gadget/g0"
	udc, err := os.ReadFile(filepath.Join(root, "UDC"))
	if err != nil || strings.TrimSpace(string(udc)) == "" {
		return errors.New("USB gadget did not bind")
	}
	linked := func(name string) bool {
		info, err := os.Lstat(filepath.Join(root, "configs/c.1", name))
		return err == nil && info.Mode()&os.ModeSymlink != 0
	}
	if linked("hid.GS0") != s.keyboard || linked("hid.GS1") != s.relative ||
		linked("hid.GS2") != s.absolute || linked("mass_storage.disk0") != s.disk ||
		linked("acm.GS0") != s.serial || (linked("rndis.usb0") || linked("ncm.usb0")) != s.network {
		return errors.New("USB functions do not match the requested composition")
	}
	return nil
}

var usbCompositionFlags = []string{
	"usb.rndis0", "usb.ncm", "usb.disk0", "usb.acm", "disable_hid",
	"usb.disable_keyboard", "usb.disable_relative", "usb.disable_absolute",
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

	// Prefer NCM when enabling a previously disabled network function. Retain
	// an explicitly selected RNDIS profile until it is disabled or switched.
	useNCM := candidate.network && (before[filepath.Join(store.bootDir, "usb.ncm")].exists ||
		!before[filepath.Join(store.bootDir, "usb.rndis0")].exists)
	flags := map[string]bool{
		"usb.rndis0": candidate.network && !useNCM, "usb.ncm": useNCM,
		"usb.disk0": candidate.disk, "usb.acm": candidate.serial,
		"disable_hid":          !candidate.keyboard && !candidate.relative && !candidate.absolute,
		"usb.disable_keyboard": !candidate.keyboard,
		"usb.disable_relative": !candidate.relative,
		"usb.disable_absolute": !candidate.absolute,
	}
	for _, name := range usbCompositionFlags {
		path := filepath.Join(store.bootDir, name)
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
