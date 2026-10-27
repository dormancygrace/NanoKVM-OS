// Copyright 2026 NanoKVM OS contributors.
// Use of this source code is governed by the BSD-style LICENSE in this directory.

//go:build gc && !purego && !appengine

package websocket

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// This firmware targets one SG2002/C906. Probe the kernel contract as well:
// the vector state must be enabled and native misaligned words must be fast.
// Go threads inherit the process's vector policy; the application must not
// disable vector state on individual threads after package initialization.
var useC906Mask = os.Getenv("NANOKVM_WEBSOCKET_MASK") != "generic" && maskC906Available()

func maskC906Available() bool {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil || !maskHasXTheadVector(string(data)) {
		return false
	}
	const (
		riscvHWProbe       = 258
		cpuPerf0           = 5
		misalignedMask     = 7
		misalignedFast     = 3
		prRISCVVGetControl = 70
		vectorCurrentMask  = 3
		vectorOn           = 2
	)
	pair := struct {
		key   int64
		value uint64
	}{key: cpuPerf0}
	_, _, errno := syscall.RawSyscall6(riscvHWProbe, uintptr(unsafe.Pointer(&pair)), 1, 0, 0, 0, 0)
	if errno != 0 || pair.key != cpuPerf0 || pair.value&misalignedMask != misalignedFast {
		return false
	}
	control, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prRISCVVGetControl, 0, 0)
	return errno == 0 && control&vectorCurrentMask == vectorOn
}

func maskHasXTheadVector(cpuinfo string) bool {
	found := false
	for _, line := range strings.Split(cpuinfo, "\n") {
		name, isa, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != "isa" {
			continue
		}
		present := false
		for _, extension := range strings.Split(strings.TrimSpace(isa), "_") {
			if extension == "xtheadvector" {
				present = true
			}
		}
		if !present {
			return false
		}
		found = true
	}
	return found
}

func maskBytes(key [4]byte, pos int, b []byte) int {
	// Keep tiny messages in Go: the assembly ABI call costs more than this loop.
	if len(b) < 8 {
		for i := range b {
			b[i] ^= key[pos&3]
			pos++
		}
		return pos & 3
	}
	if !useC906Mask {
		return maskBytesGeneric(key, pos, b)
	}
	return maskBytesC906(key, pos, b)
}

// maskBytesC906 consumes exactly b and returns (pos+len(b))&3.
// It is called only with the C906 kernel contract established above.
//
//go:noescape
func maskBytesC906(key [4]byte, pos int, b []byte) int
