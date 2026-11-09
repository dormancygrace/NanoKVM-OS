// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build gc && !purego && !nanokvmqemu

package poly1305

// useVector selects poly1305BlocksVector for long messages. NanoKVM OS runs
// only on the SG2002 (T-Head C906: XTheadVector, XTheadBa, XTheadBb,
// XTheadMac), and its kernel enables vector state for every user thread. The
// nanokvmqemu build tag keeps updateScalar for QEMU, which has no
// XTheadVector.
const useVector = true
