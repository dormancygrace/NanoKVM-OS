//go:build linux && riscv64

package webrtcdtls

import (
	"unsafe"

	"NanoKVM-Server/internal/hwcrypto"

	"github.com/pion/dtls/v3"
)

// The DTLS records of a data channel's burst are sealed together by the crypto engine: one call for up to 16
// records instead of AES-GCM in software for each (dtls.Conn.WriteBatch).
func init() {
	dtls.SetGCMRecordBatch(func(key []byte, h *[16]byte, records []dtls.GCMRecord) error {
		return hwcrypto.GCMBatch(key, h, unsafe.Pointer(&records[0]), len(records))
	})
}
