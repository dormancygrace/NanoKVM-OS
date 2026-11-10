package srtpaccel

import (
	"NanoKVM-Server/internal/hwcrypto"
	"NanoKVM-Server/internal/sha1mb"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"

	"github.com/pion/srtp/v3"
)

// frameProtector protects the packets of a video frame on the CryptoDMA
// engine: for each packet one chain of two descriptors, AES-CTR of the
// payload and then SHA-1 over header, ciphertext, rollover counter and the
// inner hash's padding, from the HMAC key's inner state (hwcrypto.CTRSHA1).
// The engine hashes 1216 bytes in 4.6 us; the multi-buffer SHA-1 on the CPU
// needs about 17 us for the HMAC of such a packet. What stays on the CPU is
// the payload's last partial AES block and the HMAC's outer block, the latter
// for the whole frame at once (sha1mb.OuterBatch).
type frameProtector struct {
	device chainEngine
	key    [16]byte
	block  cipher.Block
	auth   *sha1mb.Key
	inner  [sha1mb.Size]byte

	// Reused across frames: the SRTP context lock serializes ProtectFrame.
	packets []hwcrypto.CTRSHA1Packet
	digests [][sha1mb.Size]byte
	tags    [][sha1mb.Size]byte
}

// chainEngine is hwcrypto.Device's CTRSHA1.
type chainEngine interface {
	CTRSHA1(key *[16]byte, state *[20]byte, packets []hwcrypto.CTRSHA1Packet) int
}

func newFrameProtector(device chainEngine, encryptionKey, authKey []byte) srtp.AESCMSHA1FrameProtector {
	block, err := aes.NewCipher(encryptionKey)
	if err != nil || len(encryptionKey) != 16 {
		return nil
	}
	f := &frameProtector{device: device, block: block, auth: sha1mb.NewKey(authKey)}
	copy(f.key[:], encryptionKey)
	f.inner = f.auth.InnerState()
	return f
}

// tail XORs the payload's last partial block (the bytes after the whole
// 16-byte blocks the engine takes) with its key stream.
func (f *frameProtector) tail(p *srtp.FramePacket) {
	blocks := (p.N - p.HeaderLen) / 16
	rest := p.Buf[p.HeaderLen+16*blocks : p.N]
	if len(rest) == 0 {
		return
	}
	counter := p.IV
	// The counter block is a big-endian 128-bit number.
	low := binary.BigEndian.Uint64(counter[8:]) + uint64(blocks)
	if low < uint64(blocks) {
		binary.BigEndian.PutUint64(counter[:8], binary.BigEndian.Uint64(counter[:8])+1)
	}
	binary.BigEndian.PutUint64(counter[8:], low)
	var stream [16]byte
	f.block.Encrypt(stream[:], counter[:])
	for i := range rest {
		rest[i] ^= stream[i]
	}
}

func (f *frameProtector) ProtectFrame(packets []srtp.FramePacket) int {
	f.packets = f.packets[:0]
	for i := range packets {
		p := &packets[i]
		crypt := (p.N - p.HeaderLen) &^ 15
		// The inner hash's message is the key block, the packet and the ROC;
		// its padding goes into the packet's buffer after them.
		end := p.N + 4
		total := (end + 9 + 63) &^ 63
		if crypt == 0 || p.HeaderLen%4 != 0 || total > len(p.Buf) || total > hwcrypto.MaxPayload ||
			len(f.packets) == hwcrypto.MaxCTRSHA1 {
			break
		}
		f.tail(p)
		binary.BigEndian.PutUint32(p.Buf[p.N:], p.ROC)
		p.Buf[end] = 0x80
		clear(p.Buf[end+1 : total-8])
		binary.BigEndian.PutUint64(p.Buf[total-8:], uint64(64+end)*8)
		f.packets = append(f.packets, hwcrypto.CTRSHA1Packet{
			Data: p.Buf[:total], CryptOffset: p.HeaderLen, CryptLength: crypt, IV: p.IV,
		})
	}
	done := f.device.CTRSHA1(&f.key, &f.inner, f.packets)
	// A packet the engine did not take goes back to plaintext: the partial
	// block's key stream once more.
	for i := done; i < len(f.packets); i++ {
		f.tail(&packets[i])
	}
	if done > 0 {
		f.digests, f.tags = f.digests[:0], f.tags[:0]
		for i := range done {
			f.digests = append(f.digests, f.packets[i].State)
			f.tags = append(f.tags, [sha1mb.Size]byte{})
		}
		f.auth.OuterBatch(f.tags, f.digests)
		for i := range done {
			packets[i].Tag = f.tags[i]
		}
	}
	clear(f.packets)
	return done
}
