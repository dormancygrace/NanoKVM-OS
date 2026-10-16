package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"NanoKVM-Server/service/hid"
)

var imageBootMarkers = []string{"/boot/usb.disk0", "/boot/usb.userdisk"}

// Image mutations and gadget composition changes share the HID lock. The
// check and rename/remove must be one operation, including on case-insensitive
// FAT/exFAT. Transfers themselves never hold this lock.
func changeImage(path string, change func() error) error {
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	if err := imageUnused(path); err != nil {
		return err
	}
	return change()
}

// InstallImage publishes an upload/download only while its destination is not
// referenced by any LUN or a boot marker that a composition apply can reattach.
func InstallImage(tempPath, destPath string) error {
	return changeImage(destPath, func() error { return os.Rename(tempPath, destPath) })
}

// RemoveImage also protects startup cleanup of incomplete image transfers.
func RemoveImage(path string) error {
	return changeImage(path, func() error { return os.Remove(path) })
}

func imageUnused(path string) error {
	function := filepath.Dir(filepath.Dir(mountDevice))
	entries, err := os.ReadDir(function)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read USB storage state: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "lun.") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(function, entry.Name(), "file"))
		if err != nil {
			return fmt.Errorf("read USB medium: %w", err)
		}
		if err := differentImage(path, strings.TrimSpace(string(data))); err != nil {
			return err
		}
	}
	for _, marker := range imageBootMarkers {
		data, err := os.ReadFile(marker)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read USB configuration: %w", err)
		}
		if err := differentImage(path, strings.TrimSpace(string(data))); err != nil {
			return err
		}
	}
	return nil
}

func differentImage(path, backing string) error {
	if backing == "" {
		return nil
	}
	busy := fmt.Errorf("%s is in use by USB storage; eject it before deleting or replacing it", filepath.Base(path))
	if strings.EqualFold(filepath.Clean(path), filepath.Clean(backing)) {
		return busy
	}
	// Hard links and alternate on-disk spellings may name the same inode.
	target, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	source, err := os.Stat(backing)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if os.SameFile(target, source) {
		return busy
	}
	return nil
}
