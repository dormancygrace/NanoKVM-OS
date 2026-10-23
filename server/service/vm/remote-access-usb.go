package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/storage"
)

// Turning RustDesk on switches USB to the RustDesk preset and remembers the
// composition it replaced; turning it off puts that one back, unless the
// composition was changed in the meantime: then the user's choice stays.
var remoteAccessMarkerPath = "/etc/kvm/usb-before-rustdesk.json"

// usbFlags is a composition without its identity, which RustDesk leaves
// alone.
type usbFlags struct {
	Mode     string `json:"mode"`
	Keyboard bool   `json:"keyboard"`
	Relative bool   `json:"relative"`
	Absolute bool   `json:"absolute"`
	Network  bool   `json:"network"`
	Disk     bool   `json:"disk"`
	Serial   bool   `json:"serial"`
	Audio    bool   `json:"audio"`
	UserDisk bool   `json:"userDisk"`
}

func flagsOf(s usbComposition) usbFlags {
	return usbFlags{Mode: s.mode, Keyboard: s.keyboard, Relative: s.relative, Absolute: s.absolute,
		Network: s.network, Disk: s.disk, Serial: s.serial, Audio: s.audio, UserDisk: s.userDisk}
}

func (f usbFlags) with(identity usbIdentity) usbComposition {
	return usbComposition{mode: f.Mode, keyboard: f.Keyboard, relative: f.Relative, absolute: f.Absolute,
		network: f.Network, disk: f.Disk, serial: f.Serial, audio: f.Audio, userDisk: f.UserDisk, identity: identity}
}

// The marker: what was on before RustDesk, and what turning it on applied.
type remoteAccessMarker struct {
	Before  usbFlags `json:"before"`
	Applied usbFlags `json:"applied"`
}

func readRemoteAccessMarker(path string) (remoteAccessMarker, bool) {
	var m remoteAccessMarker
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &m) != nil {
		return remoteAccessMarker{}, false
	}
	return m, true
}

func writeRemoteAccessMarker(path string, m remoteAccessMarker) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeUSBFile(path, usbFileSnapshot{exists: true, mode: 0644, data: append(data, '\n')})
}

// remoteAccessProfile is the RustDesk preset: the three HID functions, the
// virtual disk and, when RustDesk sends sound, audio. The identity and the
// user disk carry over.
func (s usbComposition) remoteAccessProfile(withAudio bool) usbComposition {
	return usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true,
		disk: true, audio: withAudio, identity: s.identity, storageIdentity: s.storageIdentity, userDisk: s.disk && s.userDisk}
}

func enableRemoteAccessUSB(path string, current usbComposition, withAudio bool, apply func(usbComposition) error) error {
	candidate := current.remoteAccessProfile(withAudio)
	if err := candidate.validate(); err != nil {
		return err
	}
	// A marker left by a failed restore still holds the first composition
	// while nothing has changed since.
	marker, ok := readRemoteAccessMarker(path)
	if !ok || flagsOf(current) != marker.Applied {
		marker.Before = flagsOf(current)
	}
	marker.Applied = flagsOf(candidate)
	return applyWithRemoteAccessMarker(path, marker, candidate, apply)
}

// followRemoteAccessMarker records a change RustDesk itself made while on
// (its sound turned on), so turning it off still restores.
func followRemoteAccessMarker(path string, current, candidate usbComposition, apply func(usbComposition) error) error {
	marker, ok := readRemoteAccessMarker(path)
	if !ok || flagsOf(current) != marker.Applied {
		return apply(candidate)
	}
	marker.Applied = flagsOf(candidate)
	return applyWithRemoteAccessMarker(path, marker, candidate, apply)
}

// Persist recovery data before touching the gadget. If the USB transaction
// fails (it rolls itself back), restore the exact prior marker too.
func applyWithRemoteAccessMarker(path string, marker remoteAccessMarker, candidate usbComposition, apply func(usbComposition) error) error {
	previous, err := snapshotUSBFile(path)
	if err != nil {
		return err
	}
	if err := writeRemoteAccessMarker(path, marker); err != nil {
		return err
	}
	if err := apply(candidate); err != nil {
		if restoreErr := writeUSBFile(path, previous); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore RustDesk USB marker: %w", restoreErr))
		}
		return err
	}
	return nil
}

func restoreRemoteAccessUSB(path string, current usbComposition, apply func(usbComposition) error) error {
	marker, ok := readRemoteAccessMarker(path)
	if !ok {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if flagsOf(current) == marker.Applied {
		before := marker.Before.with(current.identity)
		before.storageIdentity = current.storageIdentity
		if err := before.validate(); err != nil {
			return err
		}
		// kept on failure: turning RustDesk on and off again retries
		if err := apply(before); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

// EnableRemoteAccessUSB switches USB to the RustDesk preset when RustDesk is
// turned on.
func EnableRemoteAccessUSB(withAudio bool) error {
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	current := getUSBComposition()
	return enableRemoteAccessUSB(remoteAccessMarkerPath, current, withAudio, func(candidate usbComposition) error {
		if current == candidate {
			return verifyLiveUSBComposition(candidate)
		}
		return applyLiveUSBComposition(h, current, candidate)
	})
}

// RestoreUSBAfterRemoteAccess puts back the composition RustDesk replaced.
func RestoreUSBAfterRemoteAccess() error {
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	current := getUSBComposition()
	return restoreRemoteAccessUSB(remoteAccessMarkerPath, current, func(before usbComposition) error {
		// the user disk image may have been deleted while RustDesk was on
		if before.userDisk && !storage.UserDiskExists() {
			before.userDisk = false
		}
		return applyLiveUSBComposition(h, current, before)
	})
}
