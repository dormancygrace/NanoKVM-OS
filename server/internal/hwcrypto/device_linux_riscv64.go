//go:build linux && riscv64

package hwcrypto

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// openDevice opens the module's device. Until the first boot of 3.7.0 the
// loaded module is the one from before, named sg2002_aes_probe.
func openDevice() (*os.File, error) {
	f, err := os.OpenFile("/dev/nanokvm-hwcrypto", os.O_RDWR, 0)
	if os.IsNotExist(err) {
		f, err = os.OpenFile("/dev/sg2002-aes-probe", os.O_RDWR, 0)
	}
	return f, err
}

// Open opens the root-only nanokvm-hwcrypto device. onError runs once if a request
// fails; subsequent packets use software without retrying poisoned hardware.
func Open(onError func(error)) (*Device, error) {
	f, err := openDevice()
	if err != nil {
		return nil, err
	}
	fd := f.Fd()
	const command = uintptr(3<<30) | (unsafe.Sizeof(request{}) << 16) | ('K' << 8) | 0xc1
	const chainCommand = uintptr(3<<30) | (unsafe.Sizeof(ctrSHA1Request{}) << 16) | ('K' << 8) | 0xc5
	return &Device{
		onError: onError,
		closeFD: f.Close,
		submit: func(r *request) error {
			// The module spins on the transfer (up to about 40 us for 4 KiB) and
			// sleeps only beyond that. A runtime-visible syscall that long lets
			// sysmon hand the only P to another thread; on the C906 that doubled
			// the CPU cost of a 1200-byte packet (64 to 30 us). Contention for the
			// module lock is bounded by one piece of another Crypto API user:
			// 64 KiB, about 0.7 ms for CCM (an encrypted SMB share being read).
			_, _, errno := syscall.RawSyscall(syscall.SYS_IOCTL, fd, command, uintptr(unsafe.Pointer(r)))
			runtime.KeepAlive(r)
			if errno != 0 {
				return errno
			}
			return nil
		},
		// Raw for the same reason; a frame's packets are a few hundred
		// microseconds in the engine.
		submitChain: func(r *ctrSHA1Request) error {
			_, _, errno := syscall.RawSyscall(syscall.SYS_IOCTL, fd, chainCommand, uintptr(unsafe.Pointer(r)))
			runtime.KeepAlive(r)
			if errno != 0 {
				return errno
			}
			return nil
		},
	}, nil
}
