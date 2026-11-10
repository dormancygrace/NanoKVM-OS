// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !purego

package gcm

import (
	"crypto/internal/fips140/aes"
	"crypto/internal/fips140/subtle"
	"crypto/internal/fips140deps/byteorder"
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

// c906Auth calculates GHASH(additionalData, ciphertext), masks the result
// with tagMask and writes the result to out.
func c906Auth(out []byte, g *GCM, tagMask *[gcmBlockSize]byte, ciphertext, additionalData []byte) {
	var acc [2]uint64
	c906Update(&acc, g, additionalData)
	c906Update(&acc, g, ciphertext)
	var block [gcmBlockSize]byte
	byteorder.BEPutUint64(block[:8], uint64(len(additionalData))*8)
	byteorder.BEPutUint64(block[8:], uint64(len(ciphertext))*8)
	ghashBlocksC906(&acc, &g.tables[0], &block[0], 1, &c906Reduce[0])
	byteorder.BEPutUint64(block[:8], acc[1])
	byteorder.BEPutUint64(block[8:], acc[0])
	subtle.XORBytes(out, block[:], tagMask[:])
}

// c906CounterCrypt is gcmCounterCryptGeneric with the whole blocks in one
// assembly loop (the generic loop costs 1.4 µs per block around the 0.6 µs
// block function).
func c906CounterCrypt(b *aes.Block, out, src []byte, counter *[gcmBlockSize]byte) {
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

	c906CounterCrypt(&g.cipher, out, plaintext, &counter)

	var tag [gcmTagSize]byte
	c906Auth(tag[:], g, &tagMask, out[:len(plaintext)], data)
	copy(out[len(plaintext):], tag[:])
}

func open(out []byte, g *GCM, nonce, ciphertext, data []byte) error {
	var counter, tagMask [gcmBlockSize]byte
	deriveCounterGeneric(&g.h, &counter, nonce)
	gcmCounterCryptGeneric(&g.cipher, tagMask[:], tagMask[:], &counter)

	tag := ciphertext[len(ciphertext)-g.tagSize:]
	ciphertext = ciphertext[:len(ciphertext)-g.tagSize]

	var expectedTag [gcmTagSize]byte
	c906Auth(expectedTag[:], g, &tagMask, ciphertext, data)
	if subtle.ConstantTimeCompare(expectedTag[:g.tagSize], tag) != 1 {
		return errOpen
	}

	c906CounterCrypt(&g.cipher, out, ciphertext, &counter)

	return nil
}
