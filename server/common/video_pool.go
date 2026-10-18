package common

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

var ionNodeDir = "/proc/device-tree/reserved-memory/ion"

// VideoPool is the ION video memory of a boot image: its size in whole MiB
// and whether it is reusable CMA, whose idle pages Linux may borrow (boot
// images of earlier releases; the current ones are fixed carveouts).
type VideoPool struct {
	MiB      int
	Reusable bool
}

// BootedVideoPool reads the Device Tree of the running boot image,
// independently of debugfs being mounted. A missing node is an empty pool.
func BootedVideoPool() VideoPool {
	data, err := os.ReadFile(filepath.Join(ionNodeDir, "size"))
	if err != nil || len(data) != 4 {
		return VideoPool{}
	}
	// Any doubt about the property counts as reusable: it only withholds UHD.
	_, err = os.Stat(filepath.Join(ionNodeDir, "reusable"))
	return VideoPool{MiB: int(binary.BigEndian.Uint32(data) >> 20), Reusable: !os.IsNotExist(err)}
}

// QHD is the video pool of 2560x1440 and the portrait profiles: 56 MiB, 54 in
// earlier releases. 1440p60 H.265 with MJPEG holds 53.9 MiB (see
// kvm_vision.cpp); on 54 MiB a codec change under MJPEG could find the
// carveout too fragmented and fall back to 4:2:0. Copied-frame
// fallback buffers are released as soon as native frames flow again, so
// they are not part of the steady peak.
func (p VideoPool) QHD() bool { return p.MiB >= 54 }

// PortraitMax is the 1440x2560 portrait profile (H.265 only, no MJPEG:
// 41.3 MiB); it needs the QHD pool like the other portrait profiles.
func (p VideoPool) PortraitMax() bool { return p.MiB >= 54 }

// UHD is the 104 MiB video pool that 3840x2160 needs: H.265 holds 102.7 MiB
// (see uhd_ion_mib in kvm_vision.cpp). Pools of earlier releases (112, 118
// and 128 MiB) pass too. It must be a fixed carveout: as CMA
// the encoder's UHD buffers failed on 1 of 3 cold boots under memory
// pressure (28 cma_alloc failures), when Linux's borrowed pages could not be
// migrated back.
func (p VideoPool) UHD() bool { return p.MiB >= 104 && !p.Reusable }

// MonitorFitsVideoPool reports why the saved HDMI monitor profile cannot be
// captured with a video pool, or nil when it can: the EDID advertises the
// profile to the computer, which then sends a picture that the capture path
// rejects ("Current resolution is not supported"). The landscape resolution
// counts even while portrait is on, because turning portrait off restores it.
func MonitorFitsVideoPool(p VideoPool) error {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()

	return monitorFitsVideoPoolLocked(p)
}

func monitorFitsVideoPoolLocked(p VideoPool) error {
	return savedMonitorStateLocked().FitsVideoPool(p)
}

// MonitorState is a complete HDMI monitor profile: the landscape resolution
// (0 is Auto), whether portrait is on and the portrait profile.
type MonitorState struct {
	Resolution         uint16
	Portrait           bool
	PortraitResolution uint16
}

func savedMonitorStateLocked() MonitorState {
	return MonitorState{savedMonitorResolutionLocked(), monitorPortraitEnabledLocked(), savedPortraitResolutionLocked()}
}

// MonitorStateAfter is the monitor profile that an update leads to: its
// fields, the saved settings for the others.
func MonitorStateAfter(update MonitorSettings) MonitorState {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()

	return monitorStateAfterLocked(update)
}

func monitorStateAfterLocked(update MonitorSettings) MonitorState {
	state := savedMonitorStateLocked()
	if update.Resolution != nil {
		state.Resolution = *update.Resolution
	}
	if update.Portrait != nil {
		state.Portrait = *update.Portrait
	}
	if update.PortraitResolution != nil {
		state.PortraitResolution = *update.PortraitResolution
	}
	return state
}

// FitsVideoPool reports why a video pool cannot capture the profile, or nil
// when it can. The landscape resolution counts even while portrait is on.
func (s MonitorState) FitsVideoPool(p VideoPool) error {
	switch s.Resolution {
	case 2160:
		if !p.UHD() {
			return fmt.Errorf("the monitor resolution 3840x2160 needs the UHD video memory mode")
		}
	case 1440:
		if !p.QHD() {
			return fmt.Errorf("the monitor resolution 2560x1440 needs the QHD or UHD video memory mode")
		}
	}
	if s.Portrait {
		return portraitFitsVideoPool(s.PortraitResolution, p)
	}
	return nil
}

// FitMonitorToVideoMemory lowers a saved monitor profile that the booted
// video memory cannot capture, so that the device never advertises it: 2160p
// falls back to 1440p, or to 1080p without QHD, 1440p to 1080p, and an
// unsupported portrait monitor is turned off. The video memory mode is chosen
// at boot, so this finds profiles saved under a larger mode. It reports
// whether the EDID was reprogrammed.
func FitMonitorToVideoMemory() (bool, error) {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()

	return fitMonitorToVideoPoolLocked(BootedVideoPool(), monitorSettingsBackend{
		requireMonitorHardwareLocked, portraitResolutionSupportedLocked,
		MonitorRequiresPowerCycle, MonitorRefreshFollowsStream, applyMonitorProfileLocked,
		readMonitorEDIDLocked,
	})
}

func fitMonitorToVideoPoolLocked(p VideoPool, hw monitorSettingsBackend) (bool, error) {
	var update MonitorSettings
	height := savedMonitorResolutionLocked()
	if (height == 2160 && !p.UHD()) || (height == 1440 && !p.QHD()) {
		fallback := uint16(1080)
		if height == 2160 && p.QHD() {
			fallback = 1440
		}
		update.Resolution = &fallback
	}
	if monitorPortraitEnabledLocked() && monitorFitsPortraitLocked(p) != nil {
		off := false
		update.Portrait = &off
	}
	// Without programmable EDID the receiver never advertised the profile.
	if (update.Resolution == nil && update.Portrait == nil) || hw.requireHardware() != nil {
		return false, nil
	}
	if err := applyMonitorSettingsLocked(update, hw); err != nil {
		return false, err
	}
	return true, nil
}

func monitorFitsPortraitLocked(p VideoPool) error {
	return portraitFitsVideoPool(savedPortraitResolutionLocked(), p)
}

func portraitFitsVideoPool(resolution uint16, p VideoPool) error {
	if resolution == portraitResolutionMax {
		if !p.PortraitMax() {
			return fmt.Errorf("the portrait monitor 1440x2560 needs the QHD or UHD video memory mode")
		}
	} else if !p.QHD() {
		return fmt.Errorf("the portrait monitor needs the QHD or UHD video memory mode")
	}
	return nil
}
