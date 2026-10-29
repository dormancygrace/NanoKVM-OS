//go:build linux && riscv64

package xtheadvector

import (
	"os"
	"syscall"
)

func detect() bool {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil || !isaHasExtension(data, "xtheadvector") {
		return false
	}
	const (
		prRISCVVGetControl  = 70
		prRISCVVCtrlCurMask = 3
		prRISCVVCtrlOn      = 2
	)
	state, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prRISCVVGetControl, 0, 0)
	return errno == 0 && state&prRISCVVCtrlCurMask == prRISCVVCtrlOn
}
