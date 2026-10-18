package common

import (
	"errors"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
)

// MonitorSettings describes one final HDMI monitor state. Nil fields retain
// their saved values. SyncRefresh follows the already-saved stream FPS.
type MonitorSettings struct {
	Resolution         *uint16
	Portrait           *bool
	PortraitResolution *uint16
	SyncRefresh        bool
}

// This boundary lets tests count actual programming attempts without native
// hardware, while exercising the same profile selection and persistence.
type monitorSettingsBackend struct {
	requireHardware   func() error
	portraitSupported func(uint16) bool
	powerCycle        func() bool
	followsRefresh    func() bool
	apply             func(string) error
	read              func() ([]byte, error)
}

func readMonitorEDIDLocked() ([]byte, error) { return GetKvmVision().ReadMonitorEDID() }

// Set once the first start found a Cube receiver EDID within 720p/1080p at 60 Hz.
var monitorEDIDCheckedFile = "/etc/kvm/monitor_edid_checked"

// ApplyMonitorSettings resolves all requested fields before programming the
// receiver. Intermediate orientations/resolutions are never sent to HDMI.
func ApplyMonitorSettings(update MonitorSettings) error {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	return applyMonitorSettingsLocked(update, monitorSettingsBackend{
		requireMonitorHardwareLocked, portraitResolutionSupportedLocked,
		MonitorRequiresPowerCycle, MonitorRefreshFollowsStream, applyMonitorProfileLocked,
		readMonitorEDIDLocked,
	})
}

// ProgramMonitorProfileOnFirstStart programs the saved monitor profile once
// after an installation. The HDMI receiver keeps its EDID in its own flash,
// which survives a reinstall: a receiver that advertised 2560x1440 kept doing
// so under a new system in the FHD video memory mode, and the capture refused
// the input. Until a profile is recorded, which profile the receiver holds is
// unknown. nanokvm_update_edid reads the EDID first and skips the flash write
// when the profile is already installed. It returns the programmed profile, or
// "" when a profile is recorded or EDID is not programmable. A Cube is checked
// once instead and only reprogrammed when its receiver advertises modes it
// cannot capture (checkCubeMonitorEDIDLocked).
func ProgramMonitorProfileOnFirstStart() (string, error) {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	return programMonitorProfileOnFirstStartLocked(monitorSettingsBackend{
		requireMonitorHardwareLocked, portraitResolutionSupportedLocked,
		MonitorRequiresPowerCycle, MonitorRefreshFollowsStream, applyMonitorProfileLocked,
		readMonitorEDIDLocked,
	}, BootedVideoPool())
}

func programMonitorProfileOnFirstStartLocked(hw monitorSettingsBackend, pool VideoPool) (string, error) {
	if hw.requireHardware() != nil {
		return "", nil
	}
	// Before the recorded-profile check: an image that identified a Cube as
	// PCIe recorded the 100 Hz profile it programmed there.
	if hw.powerCycle() {
		return checkCubeMonitorEDIDLocked(hw)
	}
	if _, err := os.Stat(monitorEDIDHashFile); !errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	// Fitting may have failed, or the saved state may have changed since it ran.
	// Never advertise a profile this boot cannot capture.
	if err := monitorFitsVideoPoolLocked(pool); err != nil {
		return "", err
	}
	if monitorPortraitEnabledLocked() && !hw.portraitSupported(savedPortraitResolutionLocked()) {
		return "", fmt.Errorf("portrait monitor profile is unavailable")
	}
	path := monitorProfilePathAt(savedMonitorResolutionLocked(), GetScreen().FPS)
	if monitorPortraitEnabledLocked() {
		path = portraitProfilePathAt(savedPortraitResolutionLocked(), GetScreen().FPS)
	}
	// On failure no profile is recorded, so the next start tries again.
	if err := hw.apply(path); err != nil {
		return "", err
	}
	return path, nil
}

// A Cube receiver keeps its EDID in flash across reinstalls, and writing it
// needs a power cycle, so the first start does not program it. An EDID left by
// another system or by an image that identified the board as PCIe can still
// advertise 1920x1080 at 100 Hz or larger modes, which the source then sends
// and the Cube cannot capture. Read it once and program the saved Cube profile
// only then; the power-cycle reminder follows from the write.
func checkCubeMonitorEDIDLocked(hw monitorSettingsBackend) (string, error) {
	if _, err := os.Stat(monitorEDIDCheckedFile); err == nil {
		return "", nil
	}
	edid, err := hw.read()
	if err == nil && !edidValid(edid) {
		err = errors.New("invalid EDID header or checksum")
	}
	if err != nil {
		return "", fmt.Errorf("checking the Cube receiver EDID: %w", err)
	}
	mode, beyond := edidBeyondCube(edid)
	if !beyond {
		return "", markMonitorEDIDCheckedLocked()
	}
	height := savedMonitorResolutionLocked()
	if height != 720 && height != 1080 {
		height = 0 // Auto: the Cube's 1080p profile
	}
	path := cubeMonitorProfilePath(height)
	log.Warnf("the HDMI receiver EDID advertises %s, beyond the Cube's 720p/1080p at 60 Hz; programming %s", mode, path)
	if err := hw.apply(path); err != nil {
		return "", err
	}
	if height != savedMonitorResolutionLocked() {
		if err := atomicWriteMonitorFile(monitorResolutionFile, []byte(fmt.Sprint(height)), ".monitor_resolution-*"); err != nil {
			return path, fmt.Errorf("saving monitor resolution failed: %w", err)
		}
	}
	return path, markMonitorEDIDCheckedLocked()
}

func markMonitorEDIDCheckedLocked() error {
	return atomicWriteMonitorFile(monitorEDIDCheckedFile, []byte("1\n"), ".monitor_edid_checked-*")
}

func applyMonitorSettingsLocked(update MonitorSettings, hw monitorSettingsBackend) error {
	explicit := update.Resolution != nil || update.Portrait != nil || update.PortraitResolution != nil
	refresh := update.SyncRefresh && hw.followsRefresh()
	if !explicit && !refresh {
		return nil
	}
	if err := hw.requireHardware(); err != nil {
		return err
	}
	height := savedMonitorResolutionLocked()
	portrait := monitorPortraitEnabledLocked()
	resolution := savedPortraitResolutionLocked()
	if update.Resolution != nil {
		height = *update.Resolution
		if _, ok := ResolutionMap[height]; !ok {
			return fmt.Errorf("unsupported monitor resolution")
		}
		if hw.powerCycle() && height != 0 && height != 720 && height != 1080 {
			return fmt.Errorf("Cube EDID profiles are limited to 720p/1080p at 60 Hz")
		}
	}
	if update.Portrait != nil {
		portrait = *update.Portrait
	}
	if update.PortraitResolution != nil {
		resolution = *update.PortraitResolution
	}
	if portrait || update.PortraitResolution != nil {
		if !validPortraitResolution(resolution) || !hw.portraitSupported(resolution) {
			return fmt.Errorf("portrait monitor profile is unavailable")
		}
	}
	// Saving an inactive portrait preference alone needs no HDMI write. A
	// simultaneous FPS change must still refresh the active landscape profile.
	write := update.Resolution != nil || update.Portrait != nil ||
		(portrait && update.PortraitResolution != nil)
	if write || refresh {
		path := monitorProfilePathAt(height, GetScreen().FPS)
		if portrait {
			path = portraitProfilePathAt(resolution, GetScreen().FPS)
		}
		if write || !programmedMonitorProfileLocked(path, height) {
			if err := hw.apply(path); err != nil {
				return err
			}
		}
	}
	// Persist only after successful programming; a hardware error must leave
	// the previous requested state intact.
	if update.Resolution != nil {
		if err := atomicWriteMonitorFile(monitorResolutionFile, []byte(fmt.Sprint(height)), ".monitor_resolution-*"); err != nil {
			return fmt.Errorf("saving monitor resolution failed: %w", err)
		}
	}
	if update.PortraitResolution != nil {
		if err := persistMonitorPortraitResolutionLocked(resolution); err != nil {
			return err
		}
	}
	if update.Portrait != nil {
		return persistMonitorPortraitLocked(portrait)
	}
	return nil
}
