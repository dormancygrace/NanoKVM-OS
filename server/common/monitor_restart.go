package common

import (
	"errors"
	"fmt"
	"os"
)

// ErrMonitorRestartPending refuses EDID programming while the device restarts
// into another video memory mode: the saved monitor profile belongs to the
// next boot, and the receiver must keep what it holds until then.
var ErrMonitorRestartPending = errors.New("the device is restarting with another video memory mode")

// Set while a restart into another video memory mode is pending; guarded by
// monitorMutex. Only a restart clears it, or the rollback of a failed switch.
var monitorRestartPending bool

// HoldMonitorProfileForRestart refuses every EDID write until the device
// restarts (the restart that switches the video memory mode).
func HoldMonitorProfileForRestart() {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	monitorRestartPending = true
}

// SaveMonitorSettingsForRestart saves a monitor update that the booted video
// memory cannot capture, for the video memory mode the device restarts into
// (pool). It does not program the EDID: it removes the record of the
// programmed profile, so that the first start after the restart programs the
// saved one (ProgramMonitorProfileOnFirstStart), and refuses EDID writes until
// then. restore puts the previous monitor files and record back and lifts the
// hold, for a switch that failed.
func SaveMonitorSettingsForRestart(update MonitorSettings, pool VideoPool) (restore func() error, err error) {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	return saveMonitorSettingsForRestartLocked(update, pool, monitorSettingsBackend{
		requireMonitorHardwareLocked, func(resolution uint16) bool {
			return portraitResolutionSupportedForPoolLocked(resolution, pool)
		},
		MonitorRequiresPowerCycle, MonitorRefreshFollowsStream, applyMonitorProfileLocked,
		readMonitorEDIDLocked,
	})
}

func saveMonitorSettingsForRestartLocked(update MonitorSettings, pool VideoPool, hw monitorSettingsBackend) (func() error, error) {
	if err := hw.requireHardware(); err != nil {
		return nil, err
	}
	// A Cube programs no saved profile on its first start, and its profiles
	// fit every video memory mode anyway.
	if hw.powerCycle() {
		return nil, fmt.Errorf("Cube EDID profiles are programmed at once")
	}
	state := monitorStateAfterLocked(update)
	if _, ok := ResolutionMap[state.Resolution]; !ok {
		return nil, fmt.Errorf("unsupported monitor resolution")
	}
	if state.Portrait || update.PortraitResolution != nil {
		if !validPortraitResolution(state.PortraitResolution) || !hw.portraitSupported(state.PortraitResolution) {
			return nil, fmt.Errorf("portrait monitor profile is unavailable")
		}
	}
	if err := state.FitsVideoPool(pool); err != nil {
		return nil, err
	}

	files := []string{monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile, monitorEDIDHashFile}
	saved := make([]savedMonitorFile, len(files))
	for i, path := range files {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		saved[i] = savedMonitorFile{path, data, err == nil}
	}
	restoreLocked := func() error {
		var errs []error
		for _, f := range saved {
			errs = append(errs, f.restore())
		}
		monitorRestartPending = false
		return errors.Join(errs...)
	}
	// From here on, a failure leaves the previous state: restore it.
	write := func() error {
		if update.Resolution != nil {
			if err := atomicWriteMonitorFile(monitorResolutionFile, []byte(fmt.Sprint(state.Resolution)), ".monitor_resolution-*"); err != nil {
				return fmt.Errorf("saving monitor resolution failed: %w", err)
			}
		}
		if update.PortraitResolution != nil {
			if err := persistMonitorPortraitResolutionLocked(state.PortraitResolution); err != nil {
				return err
			}
		}
		if update.Portrait != nil {
			if err := persistMonitorPortraitLocked(state.Portrait); err != nil {
				return err
			}
		}
		if err := os.Remove(monitorEDIDHashFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	monitorRestartPending = true
	if err := write(); err != nil {
		return nil, errors.Join(err, restoreLocked())
	}
	return func() error {
		monitorMutex.Lock()
		defer monitorMutex.Unlock()
		return restoreLocked()
	}, nil
}

type savedMonitorFile struct {
	path   string
	data   []byte
	exists bool
}

func (f savedMonitorFile) restore() error {
	if !f.exists {
		if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return atomicWriteMonitorFile(f.path, f.data, ".monitor_restore-*")
}
