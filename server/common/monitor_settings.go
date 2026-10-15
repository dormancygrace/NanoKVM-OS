package common

import (
	"errors"
	"fmt"
	"os"
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
}

// ApplyMonitorSettings resolves all requested fields before programming the
// receiver. Intermediate orientations/resolutions are never sent to HDMI.
func ApplyMonitorSettings(update MonitorSettings) error {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	return applyMonitorSettingsLocked(update, monitorSettingsBackend{
		requireMonitorHardwareLocked, portraitResolutionSupportedLocked,
		MonitorRequiresPowerCycle, MonitorRefreshFollowsStream, applyMonitorProfileLocked,
	})
}

// ProgramMonitorProfileOnFirstStart programs the saved monitor profile once
// after an installation. The HDMI receiver keeps its EDID in its own flash,
// which survives a reinstall: a receiver that advertised 2560x1440 kept doing
// so under a new system in the FHD video memory mode, and the capture refused
// the input. Until a profile is recorded, which profile the receiver holds is
// unknown. nanokvm_update_edid reads the EDID first and skips the flash write
// when the profile is already installed. It returns the programmed profile, or
// "" when a profile is recorded, EDID is not programmable, or the board needs
// a power cycle (Cube).
func ProgramMonitorProfileOnFirstStart() (string, error) {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	return programMonitorProfileOnFirstStartLocked(monitorSettingsBackend{
		requireMonitorHardwareLocked, portraitResolutionSupportedLocked,
		MonitorRequiresPowerCycle, MonitorRefreshFollowsStream, applyMonitorProfileLocked,
	})
}

func programMonitorProfileOnFirstStartLocked(hw monitorSettingsBackend) (string, error) {
	if _, err := os.Stat(monitorEDIDHashFile); !errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if hw.requireHardware() != nil || hw.powerCycle() {
		return "", nil
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
