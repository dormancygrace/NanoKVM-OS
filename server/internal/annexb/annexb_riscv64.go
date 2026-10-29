//go:build linux && riscv64 && !purego

package annexb

import (
	"os"
	"sync"

	"NanoKVM-Server/internal/xtheadvector"
)

// indexStartCode returns the index of the first 00 00 01 in b, or -1
// (annexb_riscv64.s). It must only be called when vector reports true.
//
//go:noescape
func indexStartCode(b []byte) int

var (
	vectorOnce sync.Once
	useVector  bool
)

// vector reports whether to use the XTheadVector search.
// NANOKVM_ANNEXB=generic selects bytes.Index.
func vector() bool {
	vectorOnce.Do(func() {
		useVector = os.Getenv("NANOKVM_ANNEXB") != "generic" && xtheadvector.Available()
	})
	return useVector
}
