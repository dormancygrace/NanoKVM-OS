//go:build riscv64 && !purego

package sha1mb

import "NanoKVM-Server/internal/xtheadvector"

// sha1x4 runs nsteps steps of the four lane queues lanes[j] = [first, end)
// of segs. Each step hashes one block per lane; see sha1mb_riscv64.s. It
// must only be called when vector reports true.
//
//go:noescape
func sha1x4(msgs *Message, scratch *byte, segs *seg, lanes *[4][2]uint32, init *[2][5]uint32, tags *[Size]byte, nsteps int)

var kernel kernelFunc = sha1x4

// vector reports whether to use the XTheadVector kernel.
func vector() bool { return xtheadvector.Available }

func haveVector() bool { return xtheadvector.Available }
