// Package xtheadvector reports whether this process may run XTheadVector
// (RVV 0.7.1) code: the T-Head C906 vector unit of the SG2002.
package xtheadvector

import (
	"bytes"
	"sync"
)

var (
	once      sync.Once
	available bool
)

// Available reports, from the first call on, whether /proc/cpuinfo lists
// xtheadvector for every hart and the kernel enables vector state on first
// use for this process (prctl PR_RISCV_V_GET_CONTROL). Go threads inherit
// that policy and the server never changes it, so the answer is cached.
func Available() bool {
	once.Do(func() { available = detect() })
	return available
}

// isaHasExtension reports whether cpuinfo (the contents of /proc/cpuinfo)
// has an "isa" line and every one lists ext among its underscore-separated
// extensions.
func isaHasExtension(cpuinfo []byte, ext string) bool {
	found := false
	for line := range bytes.Lines(cpuinfo) {
		name, isa, ok := bytes.Cut(line, []byte(":"))
		if !ok || string(bytes.TrimSpace(name)) != "isa" {
			continue
		}
		has := false
		for tok := range bytes.SplitSeq(bytes.TrimSpace(isa), []byte("_")) {
			if string(tok) == ext {
				has = true
				break
			}
		}
		if !has {
			return false
		}
		found = true
	}
	return found
}
