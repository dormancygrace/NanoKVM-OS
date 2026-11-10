// SPDX-FileCopyrightText: 2026 NanoKVM OS contributors
// SPDX-License-Identifier: MIT

package srtp

import "sync"

// FramePacket is one marshalled RTP packet of a frame handed to an
// AESCMSHA1FrameProtector: Buf[:N] is the packet, Buf[HeaderLen:N] its
// payload, IV the AES-CM counter block of its first payload block and ROC its
// rollover counter. The bytes of Buf from N on are scratch.
type FramePacket struct {
	Buf       []byte
	N         int
	HeaderLen int
	IV        [16]byte
	ROC       uint32
	Tag       [20]byte
}

// AESCMSHA1FrameProtector protects the packets of a frame under
// AES128_CM_HMAC_SHA1: for a prefix of packets, in order, it encrypts the
// payload in place with AES-128 in counter mode and sets Tag to the HMAC-SHA1
// of Buf[:N] followed by the big-endian ROC, and returns the length of that
// prefix. The packets after it must be exactly as they were up to N; the
// caller protects them itself. The SRTP context lock serializes the calls of
// one protector.
type AESCMSHA1FrameProtector interface {
	ProtectFrame(packets []FramePacket) int
}

var frameProtectorRegistry struct {
	sync.RWMutex
	factory func(encryptionKey, authKey []byte) AESCMSHA1FrameProtector
}

// SetAESCMSHA1FrameProtectorFactory selects the frame protector for
// subsequently created AES-CM contexts with a 128-bit key; nil keeps the
// per-packet cipher and the batch authenticator. The factory may return nil.
func SetAESCMSHA1FrameProtectorFactory(factory func(encryptionKey, authKey []byte) AESCMSHA1FrameProtector) {
	frameProtectorRegistry.Lock()
	frameProtectorRegistry.factory = factory
	frameProtectorRegistry.Unlock()
}

func newFrameProtector(encryptionKey, authKey []byte) AESCMSHA1FrameProtector {
	frameProtectorRegistry.RLock()
	factory := frameProtectorRegistry.factory
	frameProtectorRegistry.RUnlock()
	if factory == nil || len(encryptionKey) != 16 {
		return nil
	}
	return factory(encryptionKey, authKey)
}
