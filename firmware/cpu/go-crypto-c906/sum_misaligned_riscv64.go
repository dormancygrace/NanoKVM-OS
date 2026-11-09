// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego

package poly1305

// useMisalignedLoads lets updateScalar read unaligned messages with 64-bit
// loads instead of byte loads (TLS records put the ciphertext 5 bytes into
// the record buffer). The C906 accesses misaligned words natively and fast,
// so it is always set; it stays a variable so that the tests can also run
// the aligned-load CRYPTOGAMS variant.
var useMisalignedLoads = true
