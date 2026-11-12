//go:build linux && riscv64

package hwcrypto

import (
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// GCMBatchMax is the largest batch of GCMBatch (HWCRYPTO_GCM_BATCH_MAX).
const GCMBatchMax = 16

// Keep in sync with struct hwcrypto_gcm_batch_request of platform/modules/hwcrypto/hwcrypto.h.
type gcmBatchRequest struct {
	version, count, keyLength, reserved uint32
	key                                 [32]byte
	h                                   [16]byte
	records                             unsafe.Pointer
	reserved2                           [4]uint32
}

var gcmBatchDevice struct { //nolint:gochecknoglobals
	once sync.Once
	file *os.File
	err  error
}

// GCMBatch seals count AES-GCM messages with one key in one call of the engine driver (HWCRYPTO_GCM_BATCH):
// the DTLS records of a burst. records points to count structures laid out as struct hwcrypto_gcm_batch_record:
// the counter block J0 (16 bytes: the nonce, then 00 00 00 01), the associated data (16 bytes), its length and
// the text's length (uint32 each), and a pointer to the text, which is encrypted in place and has room for the
// 16-byte tag behind it. h is the hash key E_K(0). After an error no record may be sent as it is; the driver
// then has written none of them.
func GCMBatch(key []byte, h *[16]byte, records unsafe.Pointer, count int) error {
	d := &gcmBatchDevice
	d.once.Do(func() { d.file, d.err = openDevice() })
	if d.err != nil {
		return d.err
	}
	const command = uintptr(3<<30) | (unsafe.Sizeof(gcmBatchRequest{}) << 16) | ('K' << 8) | 0xc6
	req := gcmBatchRequest{version: 1, count: uint32(count), keyLength: uint32(len(key)), h: *h, records: records}
	copy(req.key[:], key)
	// RawSyscall: the call takes a few hundred microseconds; handing the processor over for it costs more.
	_, _, errno := syscall.RawSyscall(syscall.SYS_IOCTL, d.file.Fd(), command, uintptr(unsafe.Pointer(&req)))
	runtime.KeepAlive(records)
	req.key = [32]byte{}
	if errno != 0 {
		return errno
	}

	return nil
}
