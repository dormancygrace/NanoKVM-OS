package common

import "fmt"

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
