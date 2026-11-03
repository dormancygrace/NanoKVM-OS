package vm

import (
	"os"
	"path/filepath"
	"strconv"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/atomicfile"
)

// What the OLED (kvm_system) shows from the server: the ATX switch, 1 or 0,
// and the number of video viewers. Both are written when the server starts,
// which also ends the boot logo, and again when they change.
var (
	oledATXFile     = "/run/nanokvm/atx"
	oledViewersFile = "/run/nanokvm/viewers"
	// The viewer count last written; viewerGateMutex guards it.
	oledViewers = -1
)

// PublishOLEDState writes both at the server's start.
func PublishOLEDState() {
	publishOLEDATX()
	viewerGateMutex.Lock()
	hdmiMutex.Lock()
	viewers := hdmiDemand.ViewerCount()
	hdmiMutex.Unlock()
	publishOLEDViewers(viewers)
	viewerGateMutex.Unlock()
}

func publishOLEDATX() {
	value := 0
	if ATXEnabled() {
		value = 1
	}
	writeOLEDValue(oledATXFile, value)
}

// publishOLEDViewers runs under viewerGateMutex. A count not written is
// written again with the next update, even when it is the same.
func publishOLEDViewers(count int) {
	if count == oledViewers {
		return
	}
	if writeOLEDValue(oledViewersFile, count) {
		oledViewers = count
	}
}

func writeOLEDValue(path string, value int) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Debugf("OLED state: %v", err)
		return false
	}
	if err := atomicfile.Write(path, []byte(strconv.Itoa(value)+"\n"), 0o644); err != nil {
		log.Debugf("OLED state: %v", err)
		return false
	}
	return true
}
