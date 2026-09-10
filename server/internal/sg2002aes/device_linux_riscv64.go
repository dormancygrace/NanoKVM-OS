//go:build linux && riscv64

package sg2002aes

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// Open opens the root-only Enhanced module. onError runs once if a request
// fails; subsequent packets use software without retrying poisoned hardware.
func Open(onError func(error)) (*Device, error) {
	f, err := os.OpenFile("/dev/sg2002-aes-probe", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	fd := f.Fd()
	const command = uintptr(3<<30) | (unsafe.Sizeof(request{}) << 16) | ('K' << 8) | 0xc1
	return &Device{
		onError: onError,
		closeFD: f.Close,
		submit: func(r *request) error {
			_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, command, uintptr(unsafe.Pointer(r)))
			runtime.KeepAlive(r)
			if errno != 0 {
				return errno
			}
			return nil
		},
	}, nil
}
