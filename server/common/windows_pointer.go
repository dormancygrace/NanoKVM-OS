package common

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

const WindowsPointerMarker = "/boot/usb.pointer_windows"
const pointerContainerIDFile = "/etc/kvm/usb_container_id"

func WindowsPointerEnabled() bool {
	_, err := os.Stat(WindowsPointerMarker)
	return err == nil
}

func WindowsPointerSupported() bool {
	_, err := os.Stat("/sys/kernel/config/usb_gadget/g0/os_desc/container_id")
	return err == nil && MonitorProfileSupported() && !MonitorRequiresPowerCycle()
}

// Called while switching the USB composition. Every normal EDID change also
// passes through pointerEDIDPathLocked, retaining the same device association.
func ApplyWindowsPointerMonitor(enabled bool) error {
	monitorMutex.Lock()
	defer monitorMutex.Unlock()
	path := monitorProfilePath(savedMonitorResolutionLocked())
	if monitorPortraitEnabledLocked() {
		path = portraitMonitorEDIDPath(savedPortraitResolutionLocked())
	}
	return applyMonitorPointerProfileLocked(path, enabled)
}

func pointerContainerID() ([]byte, error) {
	text, err := os.ReadFile(pointerContainerIDFile)
	if os.IsNotExist(err) {
		id := make([]byte, 16)
		if _, err = rand.Read(id); err != nil {
			return nil, err
		}
		id[6] = (id[6] & 15) | 0x40
		id[8] = (id[8] & 63) | 0x80
		text = []byte(fmt.Sprintf("%x-%x-%x-%x-%x\n", id[:4], id[4:6], id[6:8], id[8:10], id[10:]))
		err = atomicWriteMonitorFile(pointerContainerIDFile, text, ".usb-container-*")
	}
	if err != nil {
		return nil, err
	}
	return parsePointerContainerID(string(text))
}

func parsePointerContainerID(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' {
		return nil, errors.New("invalid USB ContainerID")
	}
	id, err := hex.DecodeString(strings.ReplaceAll(text, "-", ""))
	if err != nil || len(id) != 16 || bytes.Equal(id, make([]byte, 16)) {
		return nil, errors.New("invalid USB ContainerID")
	}
	// GUID wire order used by both Microsoft OS descriptors and EDID VSDB.
	id[0], id[1], id[2], id[3] = id[3], id[2], id[1], id[0]
	id[4], id[5] = id[5], id[4]
	id[6], id[7] = id[7], id[6]
	return id, nil
}

func applyMonitorPointerProfileLocked(path string, enabled bool) error {
	if !enabled {
		return GetKvmVision().ApplyMonitorProfile(path)
	}
	if !WindowsPointerSupported() {
		return errors.New("Windows pointer requires ContainerID kernel support and live EDID programming")
	}
	id, err := pointerContainerID()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	data, err = windowsPointerEDID(data, id)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("/run", "nanokvm-pointer-*.bin")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return GetKvmVision().ApplyMonitorProfile(f.Name())
}

// Keep the preferred timing, all fallback timings, name and range descriptor.
// Dense profiles repeat the preferred DTD in CTA; deduplicate it and use the
// redundant textual serial slot for one timing instead of dropping any mode.
func windowsPointerEDID(original, id []byte) ([]byte, error) {
	if len(original) != 256 || len(id) != 16 || !bytes.Equal(original[:8], []byte{0, 255, 255, 255, 255, 255, 255, 0}) || original[126] != 1 || original[128] != 2 || original[129] != 3 {
		return nil, errors.New("unsupported EDID layout for Windows pointer")
	}
	for offset := 0; offset < 256; offset += 128 {
		var sum byte
		for _, v := range original[offset : offset+128] {
			sum += v
		}
		if sum != 0 {
			return nil, errors.New("invalid EDID checksum")
		}
	}
	end := int(original[130])
	if end < 4 || end > 127 {
		return nil, errors.New("invalid CTA boundary")
	}
	data := append([]byte(nil), original...)
	var blocks, dt []byte
	for pos := 132; pos < 128+end; {
		size := 1 + int(original[pos]&31)
		if pos+size > 128+end {
			return nil, errors.New("invalid CTA data block")
		}
		block := original[pos : pos+size]
		if !(block[0]>>5 == 3 && len(block) >= 4 && bytes.Equal(block[1:4], []byte{0x5c, 0x12, 0xca})) {
			blocks = append(blocks, block...)
		}
		pos += size
	}
	// Version 3, desktop bit 6, generic display use case 2; no HMD flags.
	blocks = append(blocks, 0x75, 0x5c, 0x12, 0xca, 3, 0x42)
	blocks = append(blocks, id...)
	for pos := 128 + end; pos+18 <= 255; pos += 18 {
		d := original[pos : pos+18]
		if d[0] == 0 && d[1] == 0 {
			break
		}
		if 4+len(blocks)+len(dt)+18 > 127 && bytes.Equal(d, original[54:72]) {
			continue
		}
		dt = append(dt, d...)
	}
	// Remove any duplicate preferred timing before considering relocation.
	for pos := 0; 4+len(blocks)+len(dt) > 127 && pos+18 <= len(dt); pos += 18 {
		if bytes.Equal(dt[pos:pos+18], original[54:72]) {
			dt = append(dt[:pos], dt[pos+18:]...)
			break
		}
	}
	for pos := 72; 4+len(blocks)+len(dt) > 127 && pos <= 108; pos += 18 {
		d := data[pos : pos+18]
		if d[0] == 0 && d[1] == 0 && (d[3] == 0x10 || d[3] == 0xff) {
			copy(d, dt[len(dt)-18:])
			dt = dt[:len(dt)-18]
		}
	}
	if 4+len(blocks)+len(dt) > 127 {
		return nil, errors.New("EDID has no room for monitor association without losing timings")
	}
	clear(data[128:])
	data[128] = 2
	data[129] = 3
	data[130] = byte(4 + len(blocks))
	data[131] = original[131] & 0xf0
	copy(data[132:], blocks)
	copy(data[132+len(blocks):], dt)
	for offset := 0; offset < 256; offset += 128 {
		var sum byte
		for _, v := range data[offset : offset+127] {
			sum += v
		}
		data[offset+127] = -sum
	}
	return data, nil
}
