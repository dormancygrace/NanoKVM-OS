//go:build riscv64 && !purego

package sha1mb

import (
	"os"
	"sync"
	"syscall"
)

// sha1x4 runs nsteps steps of the four lane queues lanes[j] = [first, end)
// of segs. Each step hashes one block per lane; see sha1mb_riscv64.s. It
// must only be called when vector reports true.
//
//go:noescape
func sha1x4(msgs *Message, scratch *byte, segs *seg, lanes *[4][2]uint32, init *[2][5]uint32, tags *[Size]byte, nsteps int)

var kernel kernelFunc = sha1x4

var (
	vectorOnce sync.Once
	useVector  bool
)

// vector reports, on first use, whether to use the XTheadVector kernel.
func vector() bool {
	vectorOnce.Do(func() { useVector = detectVector() })
	return useVector
}

// detectVector selects the kernel only if /proc/cpuinfo lists xtheadvector
// and the kernel enables vector state for this thread.
// NANOKVM_SHA1MB=generic selects crypto/sha1.
func detectVector() bool {
	if os.Getenv("NANOKVM_SHA1MB") == "generic" {
		return false
	}
	return haveVector()
}

func haveVector() bool { return cpuinfoHasExtension("xtheadvector") && vectorEnabled() }

// vectorEnabled reports whether the kernel enables vector state for this
// thread on first use (prctl PR_RISCV_V_GET_CONTROL; the sysctl
// abi.riscv_v_default_allow sets the default). Go threads inherit it.
func vectorEnabled() bool {
	const (
		prRISCVVGetControl  = 70
		prRISCVVCtrlCurMask = 3
		prRISCVVCtrlOn      = 2
	)
	r, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prRISCVVGetControl, 0, 0)
	return errno == 0 && r&prRISCVVCtrlCurMask == prRISCVVCtrlOn
}

// cpuinfoHasExtension reports whether /proc/cpuinfo has an "isa" line and
// every one lists ext among its underscore-separated extensions.
func cpuinfoHasExtension(ext string) bool {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return false
	}
	return isaHasExtension(data, ext)
}
