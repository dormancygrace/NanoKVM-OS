package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/atomicfile"
	"NanoKVM-Server/service/hid"
)

// imageBootMarkers follows hid.USBFlagsDir at call time, so that tests of
// any package can move the flags into a temporary directory.
var imageBootMarkers = func() []string {
	return []string{
		filepath.Join(hid.USBFlagsDir, "usb.disk0"),
		filepath.Join(hid.USBFlagsDir, "usb.userdisk"),
	}
}

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
// The caller has synced the bytes of tempPath.
func InstallImage(tempPath, destPath string) error {
	if err := changeImage(destPath, func() error { return os.Rename(tempPath, destPath) }); err != nil {
		return err
	}
	syncImageDir(destPath)
	return nil
}

// RemoveImage also protects startup cleanup of incomplete image transfers.
func RemoveImage(path string) error {
	if err := changeImage(path, func() error { return os.Remove(path) }); err != nil {
		return err
	}
	syncImageDir(path)
	return nil
}

// syncImageDir writes the directory entry of an installed or removed image
// to the card. /data is exFAT, which has no journal: until the directory is
// written back, a power cut undoes the rename or the unlink. It runs outside
// the HID lock, a card flush must not hold up the keyboard. The change is
// already done in the running system, so a failure is only logged.
func syncImageDir(path string) {
	if err := atomicfile.SyncDir(filepath.Dir(path)); err != nil {
		log.Warnf("sync directory of %s: %v", filepath.Base(path), err)
	}
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
	for _, marker := range imageBootMarkers() {
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
