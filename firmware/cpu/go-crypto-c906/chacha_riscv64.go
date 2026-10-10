// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

package chacha20

import (
	"sync"
	"syscall"
)

// bufSize is five blocks: xorKeyStreamC906 computes four blocks in
// XTheadVector lanes and a fifth one in scalar registers per iteration.
const bufSize = 5 * blockSize

var (
	useC906Once sync.Once
	useC906     bool
)

// c906 reports, on first use, whether to use xorKeyStreamC906.
func c906() bool {
	useC906Once.Do(func() { useC906 = detectC906() })
	return useC906
}

// detectC906 selects the XTheadVector kernel only if /proc/cpuinfo lists
// xtheadvector and the kernel enables vector state for this thread.
// NANOKVM_CHACHA20=generic selects the generic code.
func detectC906() bool {
	if env, _ := syscall.Getenv("NANOKVM_CHACHA20"); env == "generic" {
		return false
	}
	return cpuinfoHasExtension("xtheadvector") && vectorEnabled()
}

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
	fd, err := syscall.Open("/proc/cpuinfo", syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer syscall.Close(fd)
	var data []byte
	var buf [4096]byte
	for len(data) < 1<<20 {
		n, err := syscall.Read(fd, buf[:])
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return false
		}
		if n <= 0 {
			break
		}
		data = append(data, buf[:n]...)
	}
	found := false
	for len(data) > 0 {
		line := data
		if i := indexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		colon := indexByte(line, ':')
		if colon < 0 || string(trimSpace(line[:colon])) != "isa" {
			continue
		}
		if !hasToken(trimSpace(line[colon+1:]), ext) {
			return false
		}
		found = true
	}
	return found
}

func hasToken(isa []byte, ext string) bool {
	for len(isa) > 0 {
		tok := isa
		if i := indexByte(isa, '_'); i >= 0 {
			tok, isa = isa[:i], isa[i+1:]
		} else {
			isa = nil
		}
		if string(tok) == ext {
			return true
		}
	}
	return false
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// xorKeyStreamC906 needs len(dst) == len(src), a multiple of bufSize, and
// must only be called when c906 reports true.
//
//go:noescape
func xorKeyStreamC906(dst, src []byte, key *[8]uint32, nonce *[3]uint32, counter *uint32)

func (c *Cipher) xorKeyStreamBlocks(dst, src []byte) {
	if !c906() {
		c.xorKeyStreamBlocksGeneric(dst, src)
		return
	}
	if len(dst) != len(src) || len(src)%bufSize != 0 {
		panic("chacha20: internal error: wrong dst and/or src length")
	}
	xorKeyStreamC906(dst, src, &c.key, &c.nonce, &c.counter)
}
