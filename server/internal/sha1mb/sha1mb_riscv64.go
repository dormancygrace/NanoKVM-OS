//go:build riscv64 && !purego

package sha1mb

import (
	"os"
	"sync"

	"NanoKVM-Server/internal/xtheadvector"
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

// detectVector selects the kernel only if XTheadVector is available to this
// process. NANOKVM_SHA1MB=generic selects crypto/sha1.
func detectVector() bool {
	if os.Getenv("NANOKVM_SHA1MB") == "generic" {
		return false
	}
	return haveVector()
}

func haveVector() bool { return xtheadvector.Available() }
