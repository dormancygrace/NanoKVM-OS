//go:build linux && riscv64

package hwcrypto

import (
	"runtime"
	"syscall"
	"unsafe"
)

// Keep these in sync with platform/modules/hwcrypto/hwcrypto.h.
const (
	cryptoABI       = 1
	cryptoAlgSHA256 = 6
	cryptoAlgCount  = 8
)

type cryptoRequest struct {
	Version, Algorithm, Mode, Encrypt       uint32
	KeyLength, Length, OutputLength, Status uint32
	Key                                     [32]byte
	IV                                      [16]byte
	State                                   [32]byte
	Input, Output                           uint64
	Reserved                                [4]uint32
}

type cryptoInfo struct {
	Version, Algorithms, MaxInput, MaxOutput uint32
	Poisoned                                 uint32
	Reserved                                 [3]uint32
	Submitted, Completed                     [cryptoAlgCount]uint64
}

func ioc(dir, size, nr uintptr) uintptr { return dir<<30 | size<<16 | 'K'<<8 | nr }

// openSHAEngine opens the module's device for SHA-256, or returns nil when it
// is missing, poisoned or without SHA-256.
func openSHAEngine() *shaEngine {
	f, err := openDevice()
	if err != nil {
		return nil
	}
	fd := f.Fd()
	var info cryptoInfo
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioc(2, unsafe.Sizeof(info), 0xc3), uintptr(unsafe.Pointer(&info)))
	if errno != 0 || info.Version != cryptoABI || info.Poisoned != 0 ||
		info.Algorithms&(1<<cryptoAlgSHA256) == 0 || info.MaxInput < shaChunk {
		_ = f.Close()
		return nil
	}
	run := ioc(3, unsafe.Sizeof(cryptoRequest{}), 0xc2)
	return &shaEngine{run: func(state *[32]byte, blocks []byte) error {
		r := cryptoRequest{Version: cryptoABI, Algorithm: cryptoAlgSHA256, State: *state,
			Length: uint32(len(blocks)), Input: uint64(uintptr(unsafe.Pointer(&blocks[0])))}
		// Unlike SRTP packets, bulk hashing uses Syscall: a 4 KiB request
		// keeps the engine busy for about 30 us, and the only P should go to
		// the video sender meanwhile rather than wait for the loop to end.
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, run, uintptr(unsafe.Pointer(&r)))
		runtime.KeepAlive(blocks)
		runtime.KeepAlive(f)
		if errno != 0 {
			return errno
		}
		*state = r.State
		return nil
	}}
}
