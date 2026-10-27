package common

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	// The receiver may be read at start: HDMI capture is not disabled.
	readAtStart bool
}

// Set once a start found the receiver holding the recorded profile, or could
// not read it: later starts of the same boot do not reset the receiver again.
var monitorReceiverCheckedFile = "/run/nanokvm-monitor-edid-checked"

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
		readMonitorEDIDLocked, false,
	})
}

// A monitor update that a release with the video memory mode switch saved for
// the boot after the switch: the first start programs it.
var monitorDeferredFile = "/etc/kvm/monitor_edid_deferred"

// ProgramMonitorProfileOnFirstStart programs the saved monitor profile once
// after an installation. The HDMI receiver keeps its EDID in its own flash,
// which survives a reinstall: a receiver that advertised 2560x1440 kept doing
// so under a new system in the FHD video memory mode, and the capture refused
// the input. Until a profile is recorded, which profile the receiver holds is
// unknown. nanokvm_update_edid reads the EDID first and skips the flash write
// when the profile is already installed. Once a profile is recorded, the first
// start of each boot reads the receiver and programs the saved profile again only when the
// receiver holds another EDID (receiverHoldsLocked). It returns the programmed
// profile, or "" when nothing was programmed. A Cube is checked
// once instead and only reprogrammed when its receiver advertises modes it
// cannot capture (checkCubeMonitorEDIDLocked).
func ProgramMonitorProfileOnFirstStart(hdmiDisabled bool) (string, error) {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	return programMonitorProfileOnFirstStartLocked(monitorSettingsBackend{
		requireMonitorHardwareLocked, portraitResolutionSupportedLocked,
		MonitorRequiresPowerCycle, MonitorRefreshFollowsStream, applyMonitorProfileLocked,
		readMonitorEDIDLocked, !hdmiDisabled,
	}, BootedVideoPool())
}

func programMonitorProfileOnFirstStartLocked(hw monitorSettingsBackend, pool VideoPool) (string, error) {
	if hw.requireHardware() != nil {
		return "", nil
	}
	deferred := false
	if data, err := os.ReadFile(monitorDeferredFile); err == nil {
		if strings.TrimSpace(string(data)) != "1" {
			return "", fmt.Errorf("invalid deferred monitor request")
		}
		deferred = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("reading deferred monitor request: %w", err)
	}
	// A confirmed memory switch is an explicit request, unlike first-install
	// maintenance. It must finish on Cube/Lite too, after the new pool boots.
	if !deferred {
		// Before the recorded-profile check: an image that identified a Cube as
		// PCIe recorded the 100 Hz profile it programmed there. A Cube or Lite
		// with an LT6911UXC captures every profile it can hold.
		if hw.powerCycle() {
			if receiverChip() == "ux" {
				return "", nil
			}
			return checkCubeMonitorEDIDLocked(hw)
		}
		// The LT6911D on PCIe is programmed only when the user applies a profile:
		// its EDID write has not yet run on that board.
		if receiverChip() == "d" {
			return "", nil
		}
		if recorded, err := os.ReadFile(monitorEDIDHashFile); err == nil {
			hash := strings.TrimSpace(string(recorded))
			if retiredMonitorProfiles[hash] {
				if err := retireStreamRateLocked(); err != nil {
					return "", err
				}
				log.Infof("the HDMI receiver holds a retired 1080p 100 Hz profile; programming the current one")
			} else if path := rgbRangeSuccessorLocked(hash); path != "" {
				// A profile written before the profiles declared the RGB range
				// selectable: the same profile, current version.
				log.Infof("the HDMI receiver holds %s without the RGB range declaration; programming the current one", filepath.Base(path))
				if err := hw.apply(path); err != nil {
					return "", err
				}
				return path, nil
			} else if receiverHoldsLocked(hw, hash) {
				return "", nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
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
	if deferred {
		if err := os.Remove(monitorDeferredFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return path, fmt.Errorf("clearing deferred monitor request: %w", err)
		}
	}
	return path, nil
}

// Profiles that earlier releases programmed and this one no longer ships:
// 1920x1080 and 1080x1920 at 100 Hz, now 90 Hz. A receiver that still holds
// one is programmed with the current profile at the next start, as if no
// profile were recorded, and the old default stream rate of 100 fps becomes
// 90.
var retiredMonitorProfiles = map[string]bool{
	"8e90f013b8d93be6936bbcdaa324640ad9de03dd877891732c9f4daa2b95ca8a": true, // NanoKVM-monitor-1080-100.bin, also Automatic
	"2d7fd4976ab726052bcb3a17b46951f8a5d0516e01816a0061a5d98d91087bb5": true, // NanoKVM-portrait-1080x1920-100.bin
}

func retireStreamRateLocked() error {
	if GetScreen().FPS != 100 {
		return nil
	}
	if err := atomicWriteMonitorFile(screenFileMap["fps"], []byte("90"), ".fps-*"); err != nil {
		return fmt.Errorf("saving the stream rate failed: %w", err)
	}
	SetScreen("fps", 90)
	return nil
}

// The recorded profile is only what this server last wrote. The receiver keeps
// its EDID in its own flash, which a tool or another system can rewrite: a
// receiver recorded with QHD was found holding the 1080p profile, and the
// source offered no QHD while QHD showed as selected. receiverHoldsLocked reads
// the receiver (about 1 s on PCIe, with a receiver reset around the read) and
// reports false when it holds another EDID, so that the saved profile is
// programmed again. A failed read changes nothing. The read runs once per
// boot, not at every restart of the server (an update of the app restarts
// it), and never while HDMI capture is disabled: the read would release the
// receiver from reset.
func receiverHoldsLocked(hw monitorSettingsBackend, hash string) bool {
	if !hw.readAtStart {
		return true
	}
	if _, err := os.Stat(monitorReceiverCheckedFile); err == nil {
		return true
	}
	checked := func() {
		if err := os.WriteFile(monitorReceiverCheckedFile, nil, 0600); err != nil {
			log.Warnf("cannot record the HDMI receiver EDID check: %v", err)
		}
	}
	edid, err := hw.read()
	if err == nil && !edidValid(edid) {
		err = errors.New("invalid EDID header or checksum")
	}
	if err != nil {
		log.Warnf("cannot check the HDMI receiver EDID: %v", err)
		checked()
		return true
	}
	// The copy is 256 bytes; a one-block profile file is 128.
	sum := sha256.Sum256(edid[:(int(edid[126])+1)*128])
	held := hex.EncodeToString(sum[:])
	if held == hash {
		checked()
		return true
	}
	name := heldProfileNameLocked(held)
	log.Warnf("the HDMI receiver holds %s, not the recorded profile; programming the saved one", name)
	return false
}

// heldProfileNameLocked names a shipped profile with this hash for the log.
// Several files can hold the same EDID (the 1080p 90 Hz profile is also Auto
// and the final video profiles): the monitor or portrait names say most.
func heldProfileNameLocked(hash string) string {
	files, _ := filepath.Glob(filepath.Join(monitorEDIDDir, "NanoKVM-*.bin"))
	sort.Strings(files)
	var names []string
	for _, path := range files {
		if sum, err := fileSHA256(path); err == nil && sum == hash {
			names = append(names, filepath.Base(path))
		}
	}
	if len(names) == 0 {
		return "an EDID that is not a NanoKVM profile"
	}
	for _, name := range names {
		if strings.HasPrefix(name, "NanoKVM-monitor-") || strings.HasPrefix(name, "NanoKVM-portrait-") {
			return name
		}
	}
	return names[0]
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
	if height != 720 && height != 1080 && !widerMonitorProfile(height) {
		height = 0 // Auto: the Cube's 1080p profile
	}
	path := cubeMonitorProfilePath(height)
	if widerMonitorProfile(height) {
		// A 16:10 or 21:9 profile the receiver takes (saved only then) stays.
		path = monitorProfilePath(height)
	}
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
		if _, ok := ResolutionMap[height]; !ok && !widerMonitorProfile(height) {
			return fmt.Errorf("unsupported monitor resolution")
		}
		if widerMonitorProfile(height) {
			if !WiderMonitorProfileOffered(height) {
				return fmt.Errorf("this HDMI receiver does not take that 16:10 or 21:9 monitor profile")
			}
		} else if !MonitorHighRefreshSupported() && height != 0 && height != 720 && height != 1080 {
			return fmt.Errorf("HDMI receiver EDID profiles are limited to 720p/1080p at 60 Hz")
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
