//go:build linux && riscv64 && !purego

package annexb

import "NanoKVM-Server/internal/xtheadvector"

// indexStartCode returns the index of the first 00 00 01 in b, or -1
// (annexb_riscv64.s). It must only be called when vector reports true.
//
//go:noescape
func indexStartCode(b []byte) int

// vector reports whether to use the XTheadVector search.
func vector() bool { return xtheadvector.Available }
