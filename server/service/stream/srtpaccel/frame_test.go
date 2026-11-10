package srtpaccel

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"encoding"
	"encoding/binary"
	"testing"

	"NanoKVM-Server/internal/hwcrypto"

	"github.com/pion/srtp/v3"
)

// softwareChain does what HWCRYPTO_CTR_SHA1 does, for at most limit packets:
// AES-CTR over the range, then SHA-1 from the HMAC inner state over the data.
type softwareChain struct {
	authKey []byte
	limit   int
}

func (e softwareChain) CTRSHA1(key *[16]byte, _ *[20]byte, packets []hwcrypto.CTRSHA1Packet) int {
	block, _ := aes.NewCipher(key[:])
	ipad := bytes.Repeat([]byte{0x36}, 64)
	for i, b := range e.authKey {
		ipad[i] ^= b
	}
	done := min(e.limit, len(packets))
	for i := range done {
		p := &packets[i]
		if len(p.Data)%64 != 0 || p.CryptLength == 0 || p.CryptLength%16 != 0 || p.CryptOffset%4 != 0 ||
			p.CryptOffset+p.CryptLength > len(p.Data) {
			panic("a request the module refuses")
		}
		r := p.Data[p.CryptOffset : p.CryptOffset+p.CryptLength]
		cipher.NewCTR(block, p.IV[:]).XORKeyStream(r, r)
		h := sha1.New()
		h.Write(ipad)
		h.Write(p.Data)
		state, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
		copy(p.State[:], state[4:24])
	}
	return done
}

// A frame through the protector is what AES-CTR and HMAC-SHA1 give, for
// payloads with and without a partial last block; packets the engine does not
// take come back as they were.
func TestFrameProtectorMatchesAESCTRAndHMAC(t *testing.T) {
	key, authKey := bytes.Repeat([]byte{0x11}, 16), bytes.Repeat([]byte{0x33}, 20)
	block, _ := aes.NewCipher(key)
	for _, limit := range []int{64, 3, 0} {
		f := newFrameProtector(softwareChain{authKey, limit}, key, authKey)
		var packets []srtp.FramePacket
		var plain [][]byte
		sizes := []int{16, 17, 255, 256, 1188, 1200, 1403, 5, 1000}
		for i, size := range sizes {
			buf := make([]byte, 1492)
			for j := range buf[:12+size] {
				buf[j] = byte(i*31 + j)
			}
			p := srtp.FramePacket{Buf: buf, N: 12 + size, HeaderLen: 12, ROC: uint32(i) << 20}
			p.IV[3], p.IV[13] = 0x5a, byte(i)
			packets = append(packets, p)
			plain = append(plain, bytes.Clone(buf[:p.N]))
		}
		done := f.ProtectFrame(packets)
		// The packet with a 5-byte payload has no whole block for the engine.
		if want := min(limit, 7); done != want {
			t.Fatalf("limit %d: %d packets protected, want %d", limit, done, want)
		}
		for i := range packets {
			p := &packets[i]
			want := bytes.Clone(plain[i])
			if i < done {
				cipher.NewCTR(block, p.IV[:]).XORKeyStream(want[12:], want[12:])
				mac := hmac.New(sha1.New, authKey)
				mac.Write(want)
				mac.Write(binary.BigEndian.AppendUint32(nil, p.ROC))
				if !bytes.Equal(p.Tag[:], mac.Sum(nil)) {
					t.Errorf("limit %d, packet %d: wrong tag", limit, i)
				}
			}
			if !bytes.Equal(p.Buf[:p.N], want) {
				t.Errorf("limit %d, packet %d (payload %d): wrong bytes", limit, i, sizes[i])
			}
		}
	}
}
