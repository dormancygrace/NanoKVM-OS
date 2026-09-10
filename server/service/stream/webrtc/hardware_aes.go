package webrtc

import (
	"NanoKVM-Server/internal/sg2002aes"
	"os"
	"sync"

	"github.com/pion/srtp/v3"
	log "github.com/sirupsen/logrus"
)

var hardwareAESOnce sync.Once

func initializeHardwareAES() {
	hardwareAESOnce.Do(func() {
		// Process-wide selection: the descriptor is retained for the server's
		// lifetime. It is closed automatically on exit or the first ioctl error.
		if os.Getenv("NANOKVM_SRTP_AES") == "software" {
			log.Info("SRTP AES-CTR: software selected")
			return
		}
		device, err := sg2002aes.Open(func(err error) {
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
