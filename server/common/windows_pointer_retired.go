package common

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
)

// The Windows absolute-pointer profile is gone. A device updated with it on
// still has its marker and USB ContainerID, and its receiver holds the
// recorded profile with a Microsoft block that carries the same ContainerID.
var (
	retiredWindowsPointerMarker = "/boot/usb.pointer_windows"
	retiredContainerIDFile      = "/etc/kvm/usb_container_id"
)

// RetireWindowsPointer programs the recorded profile as shipped, then removes
// the marker and the ContainerID. Run it before the first-start programming.
func RetireWindowsPointer() error {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	_, err := retireWindowsPointerLocked(monitorSettingsBackend{
		requireMonitorHardwareLocked, portraitResolutionSupportedLocked,
		MonitorRequiresPowerCycle, MonitorRefreshFollowsStream, applyMonitorProfileLocked,
		readMonitorEDIDLocked, false,
	})
	return err
}

func retireWindowsPointerLocked(hw monitorSettingsBackend) (string, error) {
	if _, err := os.Stat(retiredWindowsPointerMarker); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	path := ""
	// The profile carried the block only where EDID follows the stream
	// rate: a PCIe board with an LT6911UXC.
	if hw.requireHardware() == nil && hw.followsRefresh() {
		if recorded, err := os.ReadFile(monitorEDIDHashFile); err == nil {
			hash := strings.TrimSpace(string(recorded))
			path = shippedMonitorProfileLocked(hash)
			if path == "" {
				// Written before the RGB range block: its current version,
				// so the first start has nothing left to write.
				path = rgbRangeSuccessorLocked(hash)
			}
		}
		if path == "" {
			log.Infof("the recorded monitor profile is not shipped; the HDMI receiver keeps the retired Windows pointer block until the next profile change")
		} else {
			// The marker stays until the write succeeds, so the next start
			// tries again.
			if err := hw.apply(path, savedMonitorIdentityLocked()); err != nil {
				return "", err
			}
			log.Infof("programmed %s without the retired Windows pointer block", filepath.Base(path))
		}
	}
	for _, name := range []string{retiredWindowsPointerMarker, retiredContainerIDFile} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return path, err
		}
	}
	return path, nil
}

// shippedMonitorProfileLocked is the installed profile with this hash.
func shippedMonitorProfileLocked(hash string) string {
	files, _ := filepath.Glob(filepath.Join(monitorEDIDDir, "NanoKVM-*.bin"))
	sort.Strings(files)
	// As written: with the saved monitor identity, if any.
	id := savedMonitorIdentityLocked()
	for _, path := range files {
		if sum, err := monitorProfileSHA256(path, id); err == nil && sum == hash {
			return path
		}
	}
	return ""
}
