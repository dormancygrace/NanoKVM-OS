package srtpaccel

import (
	"NanoKVM-Server/internal/hwcrypto"
	"sync"

	"github.com/pion/srtp/v3"
	log "github.com/sirupsen/logrus"
)

var hardwareAESOnce sync.Once

func initializeHardwareAES() {
	hardwareAESOnce.Do(func() {
		// Process-wide selection: the descriptor is retained for the server's
		// lifetime. It is closed automatically on exit or the first ioctl error.
		// CryptoDMA is the measured default; software remains only for a
		// missing device (the driver disabled or failed) and host builds.
		device, err := hwcrypto.Open(func(err error) {
			log.WithError(err).Warn("SRTP AES-CTR: hardware disabled; using software")
		})
		if err != nil {
			log.WithError(err).Info("SRTP AES-CTR: device unavailable; using software")
			return
		}
		srtp.SetAESCTRAccelerator(device)
		// A frame's packets go to the engine whole, cipher and hash; the
		// per-packet accelerator above serves what that leaves.
		srtp.SetAESCMSHA1FrameProtectorFactory(func(encryptionKey, authKey []byte) srtp.AESCMSHA1FrameProtector {
			return newFrameProtector(device, encryptionKey, authKey)
		})
		log.Info("SRTP AES-CTR: SG2002 packet accelerator enabled")
	})
}
