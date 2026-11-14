package vm

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/helperdisk"
	"NanoKVM-Server/internal/atomicfile"
)

// The helper disk is the read-only disk the computer sees next to the
// virtual drive (a LUN of it, S03usbdev). NanoKVM OS brings one with the
// serial console helper alone. With the computer sensors add-on the server
// puts together another, helperDiskFile, with that helper and the add-on's
// agent, and S03usbdev attaches it instead: one disk, not one per helper.
var helperDiskFile = "/var/lib/nanokvm/helper-disk.img"

// What the composed disk was last built from (sizes and times of its
// files): it is rebuilt when they change, not on every look.
var helperDiskBuiltFrom string

const helperDiskReadme = `NanoKVM helper disk
===================

This disk comes from the NanoKVM's virtual USB drive. It is read-only.

NanoKVM-Sensors-Setup-Windows.exe
    The computer sensors agent for Windows. It sends this computer's
    temperatures, voltages and fan speeds to the NanoKVM (Settings >
    Services > Computer sensors). Double-click it: it installs the agent,
    updates an older one, or removes it. It offers to install the PawnIO
    driver, which processor and board sensors need.
    From an administrator's console instead:
        NanoKVM-Sensors-Setup-Windows.exe install
        NanoKVM-Sensors-Setup-Windows.exe uninstall
        NanoKVM-Sensors-Setup-Windows.exe --help

NanoKVM-Serial-Console-Setup-Linux.sh
    A login console on the NanoKVM's USB serial port for a Linux computer.
    As root, with this disk mounted on /mnt:
        sh /mnt/NanoKVM-Serial-Console-Setup-Linux.sh          report only
        sh /mnt/NanoKVM-Serial-Console-Setup-Linux.sh apply    make the changes
        sh /mnt/NanoKVM-Serial-Console-Setup-Linux.sh undo     remove them
`

// The names say what each file sets up, and for which system.
const (
	helperDiskConsoleName = "NanoKVM-Serial-Console-Setup-Linux.sh"
	helperDiskSensorsName = "NanoKVM-Sensors-Setup-Windows.exe"
)

// helperDiskImage is the image S03usbdev attaches as the helper disk: the
// composed one when there is one, the build's otherwise.
func helperDiskImage() string {
	if info, err := os.Stat(helperDiskFile); err == nil && info.Mode().IsRegular() {
		return helperDiskFile
	}
	return serialConsoleDiskFile()
}

// helperDiskLun reports the helper LUN: attached (it has one of the two
// images) and stale (the computer sees another image than the current one,
// until the USB devices reconnect).
func helperDiskLun() (attached, stale bool) {
	data, err := os.ReadFile(serialConsoleLunFile)
	if err != nil {
		return false, false
	}
	lun := strings.TrimSpace(string(data))
	path := strings.TrimSuffix(lun, " (deleted)")
	if path != helperDiskFile && path != serialConsoleDiskFile() {
		return false, false
	}
	return true, path != lun || path != helperDiskImage()
}

func fileSignature(paths ...string) string {
	var b strings.Builder
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "%s -\n", path)
		}
	}
	return b.String()
}

// reconcileHelperDisk keeps the composed disk in step with the add-on:
// built while the add-on is installed, removed with it. The LUN keeps the
// image it has until the USB devices reconnect; helperDiskLun says so.
func reconcileHelperDisk() {
	if !HostSensorsInstalled() {
		helperDiskBuiltFrom = ""
		if err := os.Remove(helperDiskFile); err != nil && !os.IsNotExist(err) {
			log.Errorf("helper disk: %v", err)
		}
		return
	}
	signature := fileSignature(hostSensorsAgentFile(), serialConsoleScriptFile())
	if _, err := os.Stat(helperDiskFile); err == nil && signature == helperDiskBuiltFrom {
		return
	}
	agent, err := os.ReadFile(hostSensorsAgentFile())
	if err != nil {
		log.Errorf("helper disk: %v", err)
		return
	}
	files := []helperdisk.File{{Name: "README.txt", Data: []byte(strings.ReplaceAll(helperDiskReadme, "\n", "\r\n"))}}
	// The console helper is part of NanoKVM OS; an image without it is
	// still better than none.
	if script, err := os.ReadFile(serialConsoleScriptFile()); err == nil {
		files = append(files, helperdisk.File{Name: helperDiskConsoleName, Data: script})
	}
	files = append(files, helperdisk.File{Name: helperDiskSensorsName, Data: agent})
	image, err := helperdisk.Build(files)
	if err != nil {
		log.Errorf("helper disk: %v", err)
		return
	}
	// After a restart the disk is usually the same: the card is not written.
	if old, err := os.ReadFile(helperDiskFile); err != nil || !bytes.Equal(old, image) {
		if err := os.MkdirAll(filepath.Dir(helperDiskFile), 0o755); err == nil {
			err = atomicfile.Write(helperDiskFile, image, 0o644)
		}
		if err != nil {
			log.Errorf("helper disk: %v", err)
			return
		}
		log.Infof("helper disk: built with the computer sensors agent")
	}
	helperDiskBuiltFrom = signature
}
