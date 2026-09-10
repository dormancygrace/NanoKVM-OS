// SPDX-FileCopyrightText: 2026 NanoKVM Enhanced contributors
// SPDX-License-Identifier: MIT

package srtp

import (
	"crypto/aes"
	"crypto/cipher"
	"sync"
)

// AESCTRAccelerator optionally transforms a complete AES-128 CTR payload.
// Implementations must be safe for concurrent use. Returning false requests
// software fallback and MUST leave dst, src, key and iv unchanged. Returning
// true must write exactly len(src) bytes to dst and leave key and iv unchanged.
// Exact src/dst overlap is supported. Implementations must not retain slices.
type AESCTRAccelerator interface {
	TryXORKeyStream(key, iv, dst, src []byte) bool
}

var aesCTRRegistry struct {
	sync.RWMutex
	backend AESCTRAccelerator
}

// SetAESCTRAccelerator selects the accelerator for subsequently created AES-CM
// contexts. Existing contexts keep their selection. nil selects software only.
// Call before creating WebRTC connections. Key derivation, GCM, NULL, counters,
// authentication and replay protection are unaffected.
func SetAESCTRAccelerator(backend AESCTRAccelerator) {
	aesCTRRegistry.Lock()
	aesCTRRegistry.backend = backend
	aesCTRRegistry.Unlock()
}

type acceleratedAESCMBlock struct {
	cipher.Block
	key     [16]byte
	backend AESCTRAccelerator
}

func newAESCMBlock(key []byte) (cipher.Block, error) {
	block, err := aes.NewCipher(key)
	if err != nil || len(key) != 16 {
		return block, err
	}
	aesCTRRegistry.RLock()
	backend := aesCTRRegistry.backend
	aesCTRRegistry.RUnlock()
	if backend == nil {
		return block, nil
	}
	wrapped := &acceleratedAESCMBlock{Block: block, backend: backend}
	copy(wrapped.key[:], key)
	return wrapped, nil
}
