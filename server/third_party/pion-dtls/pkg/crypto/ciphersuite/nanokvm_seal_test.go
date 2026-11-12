// SPDX-License-Identifier: MIT

package ciphersuite

import (
	"bytes"
	"testing"

	"github.com/pion/dtls/v3/pkg/protocol"
	"github.com/pion/dtls/v3/pkg/protocol/recordlayer"
)

// SealApplicationData must make the bytes Encrypt makes of the marshaled record, for both suites NanoKVM
// negotiates, and leave what is in front of the record alone.
func TestSealApplicationDataMatchesEncrypt(t *testing.T) {
	key := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(7*i + n)
		}

		return b
	}
	gcm, err := NewGCM(key(16), key(4), key(16), key(4))
	if err != nil {
		t.Fatal(err)
	}
	chacha, err := NewChaCha20Poly1305(key(32), key(12), key(32), key(12))
	if err != nil {
		t.Fatal(err)
	}
	type suite interface {
		Encrypt(pkt *recordlayer.RecordLayer, raw []byte) ([]byte, error)
		Decrypt(header recordlayer.Header, in []byte) ([]byte, error)
		SealApplicationData(dst []byte, epoch uint16, sequenceNumber uint64, payload []byte) []byte
	}
	for name, s := range map[string]suite{"AES-GCM": gcm, "ChaCha20-Poly1305": chacha} {
		for _, size := range []int{0, 1, 15, 16, 17, 1199, 1200, 16000} {
			for _, seq := range []uint64{0, 1, 0x0000fffffffffffe, recordlayer.MaxSequenceNumber} {
				payload := key(size)
				record := &recordlayer.RecordLayer{
					Header:  recordlayer.Header{Epoch: 1, Version: protocol.Version1_2, SequenceNumber: seq},
					Content: &protocol.ApplicationData{Data: payload},
				}
				raw, err := record.Marshal()
				if err != nil {
					t.Fatal(err)
				}
				want, err := s.Encrypt(record, raw)
				if err != nil {
					t.Fatal(err)
				}
				got := s.SealApplicationData([]byte{0xa5, 0x5a, 0xc3}, 1, seq, payload)
				if !bytes.Equal(got[:3], []byte{0xa5, 0x5a, 0xc3}) || !bytes.Equal(got[3:], want) {
					t.Fatalf("%s, %d bytes, sequence %#x: the record differs from Encrypt's", name, size, seq)
				}
				plain, err := s.Decrypt(recordlayer.Header{}, got[3:])
				if err != nil || !bytes.Equal(plain[recordlayer.FixedHeaderSize:], payload) {
					t.Fatalf("%s, %d bytes, sequence %#x: the record does not open: %v", name, size, seq, err)
				}
			}
		}
	}
}
