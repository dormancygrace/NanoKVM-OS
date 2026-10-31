package srtpaccel

import (
	"NanoKVM-Server/internal/hwcrypto"
	"os"
	"strings"
	"sync"

	"github.com/pion/srtp/v3"
	log "github.com/sirupsen/logrus"
)

var hardwareAESOnce sync.Once
var openHardwareAES = hwcrypto.Open

// The module, and until the first boot of 3.7.0 the one named before it.
var hardwareAESCapabilityFiles = []string{"/sys/module/nanokvm_hwcrypto/parameters/out_of_place_burst4",
	"/sys/module/sg2002_aes_probe/parameters/out_of_place_burst4"}

// Read the loaded module, not its replacement on disk: component updates
// must not send video traffic to an old CryptoDMA implementation before reboot.
func hardwareAESDriverReady() bool {
	for _, file := range hardwareAESCapabilityFiles {
		if data, err := os.ReadFile(file); err == nil {
			return strings.TrimSpace(string(data)) == "Y"
		}
	}
	return false
}

func initializeHardwareAES() {
	hardwareAESOnce.Do(func() {
		// Process-wide selection: the descriptor is retained for the server's
		// lifetime. It is closed automatically on exit or the first ioctl error.
		if os.Getenv("NANOKVM_SRTP_AES") != "hardware" {
			log.Info("SRTP AES-CTR: software selected")
			return
		}
		if !hardwareAESDriverReady() {
			log.Warn("SRTP AES-CTR: loaded CryptoDMA module lacks out-of-place/burst4 support; using software until the updated module is loaded and the app restarts")
			return
		}
		device, err := openHardwareAES(func(err error) {
			log.WithError(err).Warn("SRTP AES-CTR: hardware disabled; using software")
		})
		if err != nil {
			log.WithError(err).Info("SRTP AES-CTR: device unavailable; using software")
			return
		}
		srtp.SetAESCTRAccelerator(device)
		log.Info("SRTP AES-CTR: SG2002 packet accelerator enabled")
	})
}
