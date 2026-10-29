//go:build linux && riscv64 && !purego

package logs

import (
	"os"
	"strings"
	"sync"

	"NanoKVM-Server/internal/xtheadvector"
)

var logVectorOnce sync.Once
var logUseVector bool

// The server does not change per-thread vector policy, so the answer is cached.
func logVectorAvailable() bool {
	logVectorOnce.Do(func() { logUseVector = detectLogVector() })
	return logUseVector
}

func detectLogVector() bool {
	// Diagnostic override permits a same-binary comparison in separate processes.
	if os.Getenv("NANOKVM_LOG_TEXT") == "generic" {
		return false
	}
	return xtheadvector.Available()
}

func sensitiveLine(line string) bool {
	if preferScalarLogLine(line) || !logVectorAvailable() {
		return sensitiveLineGeneric(line)
	}
	lowered := strings.ToLower(line)
	for _, word := range sensitiveWords {
		if containsKeywordVector(lowered, word) {
			return true
		}
	}
	return containsKeywordVector(lowered, "http") && sensitiveEntry.MatchString(line)
}

func needsRuneFilter(s string) bool {
	if len(s) < 8 || !logVectorAvailable() {
		return needsRuneFilterGeneric(s)
	}
	switch {
	case len(s) <= 16:
		return logNeedsM1(s)
	case len(s) <= 32:
		return logNeedsM2(s)
	case len(s) <= 64:
		return logNeedsM4(s)
	default:
		return logNeedsM8(s)
	}
}

func containsEscape(s string) bool {
	if len(s) < 8 || !logVectorAvailable() {
		return strings.IndexByte(s, 0x1b) >= 0
	}
	return logIndexByteVector(s, 0x1b) >= 0
}

func logIndexByteVector(s string, c byte) int {
	switch {
	case len(s) <= 16:
		return logIndexM1(s, c)
	case len(s) <= 32:
		return logIndexM2(s, c)
	case len(s) <= 64:
		return logIndexM4(s, c)
	default:
		return logIndexM8(s, c)
	}
}

// containsKeywordVector is only reached after the vector gate. It searches for
// first/last-byte candidates without reading beyond the string. After one false
// candidate, short lines finish scalarly; long lines use the standard search.
func containsKeywordVector(s, sub string) bool {
	n := len(sub)
	if n < 2 || len(s) < 16 {
		return strings.Contains(s, sub)
	}
	if n > len(s) {
		return false
	}
	remaining := len(s) - n + 1
	var i int
	switch {
	case remaining <= 16:
		i = logPairM1(s, sub[0], sub[n-1], n-1)
	case remaining <= 32:
		i = logPairM2(s, sub[0], sub[n-1], n-1)
	case remaining <= 64:
		i = logPairM4(s, sub[0], sub[n-1], n-1)
	default:
		i = logPairM8(s, sub[0], sub[n-1], n-1)
	}
	if i < 0 {
		return false
	}
	if s[i:i+n] == sub {
		return true
	}
	if len(s) <= 128 {
		return containsKeywordScalar(s[i+1:], sub)
	}
	return strings.Contains(s[i+1:], sub)
}

// Leaf RVV 0.7.1 kernels; Go ABI0 arguments and results stay on the stack.

//go:noescape
func logNeedsM1(s string) bool

//go:noescape
func logIndexM1(s string, value byte) int

//go:noescape
func logPairM1(s string, first, last byte, distance int) int

//go:noescape
func logNeedsM2(s string) bool

//go:noescape
func logIndexM2(s string, value byte) int

//go:noescape
func logPairM2(s string, first, last byte, distance int) int

//go:noescape
func logNeedsM4(s string) bool

//go:noescape
func logIndexM4(s string, value byte) int

//go:noescape
func logPairM4(s string, first, last byte, distance int) int

//go:noescape
func logNeedsM8(s string) bool

//go:noescape
func logIndexM8(s string, value byte) int

//go:noescape
func logPairM8(s string, first, last byte, distance int) int
