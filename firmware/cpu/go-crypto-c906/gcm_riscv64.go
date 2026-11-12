// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !purego

package gcm

import (
	"crypto/internal/fips140/aes"
	"crypto/internal/fips140/subtle"
	"crypto/internal/fips140deps/byteorder"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// GHASH for the T-Head C906 with 8-bit tables (ghash_c906_riscv64.s, generated
// by gen_ghash_table.py). NanoKVM OS runs only on this CPU: there is no
// selection. The generic GHASH multiplies with integer multiplications, which
// takes the C906 about 8 µs per block; two tables of 4 KiB per key take
// 0.15 µs. Table lookups depend on the key and the data: unlike the generic
// code this is not constant-time.
//
// acc is {low, high}: low is the big-endian value of bytes 8..15 of the GHASH
// state, high of bytes 0..7.

//go:noescape
func ghashBlocksC906(acc *[2]uint64, tables *uint64, data *byte, nblocks int, rtab *uint64)

// c906Reduce[r] is what the 8 bits r shifted out of the low end fold back
// into the top 16 bits.
var c906Reduce [256]uint64

func init() {
	for r := 0; r < 256; r++ {
		var v uint64
		for j := 0; j < 8; j++ {
			if r>>j&1 != 0 {
				v ^= 0xe100 >> (7 - j)
			}
		}
		c906Reduce[r] = v << 48
	}
}

type gcmPlatformData struct {
	// Table q is 256 low words then 256 high words; entry b is
	// b * H * x^(64 q), where bit 7 of b is the coefficient of the lowest
	// power.
	tables [2 * 512]uint64
	h      [gcmBlockSize]byte
}

func checkGenericIsExpected() {}

// c906MulX multiplies by x in the bit order of GHASH.
func c906MulX(hi, lo uint64) (uint64, uint64) {
	carry := lo & 1
	lo = hi<<63 | lo>>1
	hi >>= 1
	if carry != 0 {
		hi ^= 0xe100000000000000
	}
	return hi, lo
}

func initGCM(g *GCM) {
	aes.EncryptBlockInternal(&g.cipher, g.h[:], g.h[:])
	hi, lo := byteorder.BEUint64(g.h[:8]), byteorder.BEUint64(g.h[8:])
	for q := 0; q < 2; q++ {
		tl, th := g.tables[512*q:512*q+256], g.tables[512*q+256:512*q+512]
		for i := 0x80; i != 0; i >>= 1 {
			tl[i], th[i] = lo, hi
			hi, lo = c906MulX(hi, lo)
		}
		for i := 2; i < 256; i <<= 1 {
			for j := 1; j < i; j++ {
				tl[i+j], th[i+j] = tl[i]^tl[j], th[i]^th[j]
			}
		}
		for i := 8; i < 64; i++ {
			hi, lo = c906MulX(hi, lo)
		}
	}
}

func c906Update(acc *[2]uint64, g *GCM, data []byte) {
	if n := len(data) / gcmBlockSize; n > 0 {
		ghashBlocksC906(acc, &g.tables[0], &data[0], n, &c906Reduce[0])
		data = data[n*gcmBlockSize:]
	}
	if len(data) > 0 {
		var block [gcmBlockSize]byte
		copy(block[:], data)
		ghashBlocksC906(acc, &g.tables[0], &block[0], 1, &c906Reduce[0])
	}
}

// c906Tag finishes GHASH over lengths of additional data and ciphertext,
// masks the result with tagMask and writes it to out.
func c906Tag(out []byte, g *GCM, acc *[2]uint64, tagMask *[gcmBlockSize]byte, ciphertextLen, additionalDataLen int) {
	var block [gcmBlockSize]byte
	byteorder.BEPutUint64(block[:8], uint64(additionalDataLen)*8)
	byteorder.BEPutUint64(block[8:], uint64(ciphertextLen)*8)
	ghashBlocksC906(acc, &g.tables[0], &block[0], 1, &c906Reduce[0])
	byteorder.BEPutUint64(block[:8], acc[1])
	byteorder.BEPutUint64(block[8:], acc[0])
	subtle.XORBytes(out, block[:], tagMask[:])
}

// The crypto engine of the SG2002 (/dev/nanokvm-hwcrypto, the module
// nanokvm_hwcrypto of the same image). Go's AES takes the C906 about 40 µs
// per KiB; the engine about 3, plus some 15 µs per request. As in OpenSSL:
// from engineCTRMin bytes the counter mode goes to the engine and GHASH stays
// here; from engineGCMMin bytes the driver does both, hashing on the CPU
// while the engine encrypts.
//
// The requests are raw system calls (the scheduler is not told): they
// take 20 to 300 µs of this CPU (the driver spins on the engine and hashes)
// and do not wait for anything else.
const (
	engineCTRMin = 1024
	engineGCMMin = 4096
	engineMax    = 4096 // one HWCRYPTO_RUN of the counter mode
	// HWCRYPTO_GCM on the caller's memory starts at this length in the
	// driver; shorter requests are done on its own copy.
	engineInPlaceMin = 8192
)

// engineState is 0 before the first use, the descriptor plus one, or -1: no
// engine for this process (no device, no permission, or the driver gave it
// up).
var engineState atomic.Int32

func engineFD() int {
	for {
		s := engineState.Load()
		if s != 0 {
			return int(s) - 1 // -2 when there is none
		}
		path := [...]byte{'/', 'd', 'e', 'v', '/', 'n', 'a', 'n', 'o', 'k', 'v', 'm', '-', 'h', 'w', 'c', 'r', 'y', 'p', 't', 'o', 0}
		const atFDCWD, oRDWR, oCLOEXEC = ^uintptr(99), 2, 0x80000
		fd, _, errno := syscall.RawSyscall6(syscall.SYS_OPENAT, atFDCWD, uintptr(unsafe.Pointer(&path[0])), oRDWR|oCLOEXEC, 0, 0, 0)
		if errno != 0 {
			engineState.CompareAndSwap(0, -1)
			continue
		}
		if !engineState.CompareAndSwap(0, int32(fd)+1) {
			syscall.RawSyscall(syscall.SYS_CLOSE, fd, 0, 0)
		}
	}
}

// engineFault says whether the request failed because the engine is lost
// (EIO, ETIMEDOUT: until the reboot), and gives the engine up for this
// process unless the failure is one of this call only.
func engineFault(errno syscall.Errno) (lost bool) {
	const eINTR, eIO, eAGAIN, eNOMEM, eFAULT, eTIMEDOUT = 4, 5, 11, 12, 14, 110
	switch errno {
	case eINTR, eAGAIN, eNOMEM, eFAULT:
		return false
	}
	engineState.Store(-1)
	return errno == eIO || errno == eTIMEDOUT
}

// struct hwcrypto_request and struct hwcrypto_gcm_request of the driver's
// hwcrypto.h.
type engineRequest struct {
	version, algorithm, mode, encrypt       uint32
	keyLength, length, outputLength, status uint32
	key                                     [32]byte
	iv                                      [16]byte
	state                                   [32]byte
	input, output                           uint64
	reserved                                [4]uint32
}

type engineGCMRequest struct {
	version, encrypt, keyLength, length uint32
	key                                 [32]byte
	h, xi, ctr                          [16]byte
	input, output                       uint64
	reserved                            [4]uint32
}

const (
	engineIoctl  = 29
	engineRun    = 0xc0904bc2 // HWCRYPTO_RUN
	engineRunGCM = 0xc0804bc4 // HWCRYPTO_GCM
)

// engineCTR is the counter mode over the whole blocks of src through the
// engine, in requests of up to engineMax bytes. It returns the bytes done and
// advances counter over them; the caller does the rest. A request this short
// leaves dst untouched when it fails.
func engineCTR(b *aes.Block, dst, src []byte, counter *[gcmBlockSize]byte) int {
	fd := engineFD()
	if fd < 0 {
		return 0
	}
	var r engineRequest
	r.version, r.algorithm, r.mode, r.encrypt = 1, 1, 2, 1 // AES, CTR
	r.keyLength = uint32(aes.KeyC906(b, &r.key))
	done := 0
	for len(src)-done >= engineCTRMin {
		n := min(len(src)-done, engineMax) &^ (gcmBlockSize - 1)
		ctr := byteorder.BEUint32(counter[12:])
		// GCM's counter is the last 32 bits; the engine counts in 128.
		if uint64(ctr)+uint64(n/gcmBlockSize) > 1<<32 {
			break
		}
		r.length, r.outputLength = uint32(n), uint32(n)
		r.iv = *counter
		r.input = uint64(uintptr(unsafe.Pointer(&src[done])))
		r.output = uint64(uintptr(unsafe.Pointer(&dst[done])))
		_, _, errno := syscall.RawSyscall6(engineIoctl, uintptr(fd), engineRun, uintptr(unsafe.Pointer(&r)), 0, 0, 0)
		if errno != 0 {
			engineFault(errno)
			break
		}
		byteorder.BEPutUint32(counter[12:], ctr+uint32(n/gcmBlockSize))
		done += n
	}
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
	clear(r.key[:])
	return done
}

// engineGCM is HWCRYPTO_GCM over src, a multiple of the block size: the
// counter mode from counter and GHASH of the ciphertext into acc, both
// advanced over the bytes done, which it returns. lost: the engine was lost
// in a request the driver does in place, and dst is partly written.
func engineGCM(g *GCM, encrypt bool, acc *[2]uint64, counter *[gcmBlockSize]byte, dst, src []byte) (done int, lost bool) {
	fd := engineFD()
	if fd < 0 {
		return 0, false
	}
	var r engineGCMRequest
	r.version = 1
	if encrypt {
		r.encrypt = 1
	}
	r.keyLength = uint32(aes.KeyC906(&g.cipher, &r.key))
	r.length = uint32(len(src))
	r.h = g.h
	byteorder.BEPutUint64(r.xi[:8], acc[1])
	byteorder.BEPutUint64(r.xi[8:], acc[0])
	r.ctr = *counter
	r.input = uint64(uintptr(unsafe.Pointer(&src[0])))
	r.output = uint64(uintptr(unsafe.Pointer(&dst[0])))
	_, _, errno := syscall.RawSyscall6(engineIoctl, uintptr(fd), engineRunGCM, uintptr(unsafe.Pointer(&r)), 0, 0, 0)
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
	clear(r.key[:])
	if errno != 0 {
		lost = engineFault(errno) && r.input == r.output && len(src) >= engineInPlaceMin
		return 0, lost
	}
	acc[1], acc[0] = byteorder.BEUint64(r.xi[:8]), byteorder.BEUint64(r.xi[8:])
	*counter = r.ctr
	return int(r.length), false
}

// c906CounterCrypt is gcmCounterCryptGeneric with the whole blocks through
// the engine or in one assembly loop (the generic loop costs 1.4 µs per block
// around the 0.6 µs block function).
func c906CounterCrypt(b *aes.Block, out, src []byte, counter *[gcmBlockSize]byte) {
	if len(src) >= engineCTRMin {
		n := engineCTR(b, out, src, counter)
		out, src = out[n:], src[n:]
	}
	if n := len(src) &^ (gcmBlockSize - 1); n > 0 {
		aes.CTR32BlocksC906(b, out[:n], src[:n], counter)
		out, src = out[n:], src[n:]
	}
	if len(src) > 0 {
		gcmCounterCryptGeneric(b, out, src, counter)
	}
}

func seal(out []byte, g *GCM, nonce, plaintext, data []byte) {
	var counter, tagMask [gcmBlockSize]byte
	deriveCounterGeneric(&g.h, &counter, nonce)
	gcmCounterCryptGeneric(&g.cipher, tagMask[:], tagMask[:], &counter)

	var acc [2]uint64
	c906Update(&acc, g, data)
	done := 0
	if n := len(plaintext) &^ (gcmBlockSize - 1); n >= engineGCMMin {
		var lost bool
		done, lost = engineGCM(g, true, &acc, &counter, out[:n], plaintext[:n])
		if lost {
			// The plaintext is partly ciphertext now: nothing correct
			// can be returned, and Seal has no error.
			panic("crypto/cipher: the crypto engine was lost in a request in place")
		}
	}
	c906CounterCrypt(&g.cipher, out[done:], plaintext[done:], &counter)
	c906Update(&acc, g, out[done:len(plaintext)])

	var tag [gcmTagSize]byte
	c906Tag(tag[:], g, &acc, &tagMask, len(plaintext), len(data))
	copy(out[len(plaintext):], tag[:])
}

// open decrypts before it knows the tag: Open clears out when it fails.
func open(out []byte, g *GCM, nonce, ciphertext, data []byte) error {
	var counter, tagMask [gcmBlockSize]byte
	deriveCounterGeneric(&g.h, &counter, nonce)
	gcmCounterCryptGeneric(&g.cipher, tagMask[:], tagMask[:], &counter)

	tag := ciphertext[len(ciphertext)-g.tagSize:]
	ciphertext = ciphertext[:len(ciphertext)-g.tagSize]

	var acc [2]uint64
	c906Update(&acc, g, data)
	done := 0
	if n := len(ciphertext) &^ (gcmBlockSize - 1); n >= engineGCMMin {
		var lost bool
		done, lost = engineGCM(g, false, &acc, &counter, out[:n], ciphertext[:n])
		if lost {
			return errOpen
		}
	}
	// The rest may be in place: its hash before its plaintext.
	c906Update(&acc, g, ciphertext[done:])

	var expectedTag [gcmTagSize]byte
	c906Tag(expectedTag[:], g, &acc, &tagMask, len(ciphertext), len(data))
	if subtle.ConstantTimeCompare(expectedTag[:g.tagSize], tag) != 1 {
		clear(out[:done])
		return errOpen
	}

	c906CounterCrypt(&g.cipher, out[done:], ciphertext[done:], &counter)

	return nil
}
