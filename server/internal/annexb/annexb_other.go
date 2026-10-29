//go:build !(linux && riscv64) || purego

package annexb

import "bytes"

func vector() bool { return false }

func indexStartCode(b []byte) int { return bytes.Index(b, startCode) }
