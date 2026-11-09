// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego && !nanokvmqemu

package chacha20

// useC906 selects xorKeyStreamC906. NanoKVM OS runs only on the SG2002
// (T-Head C906: XTheadVector and XTheadBb), and its kernel enables vector
// state for every user thread. The nanokvmqemu build tag selects the
// generic code for QEMU, which has no XTheadVector.
const useC906 = true
