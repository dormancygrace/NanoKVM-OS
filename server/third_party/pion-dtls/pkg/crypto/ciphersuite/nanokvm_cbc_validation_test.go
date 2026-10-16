package ciphersuite

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"github.com/pion/dtls/v3/pkg/protocol"
	"github.com/pion/dtls/v3/pkg/protocol/recordlayer"
	"testing"
)

func TestNanoKVMCBCRejectsMalformedRecords(t *testing.T) {
	key, iv, macKey := make([]byte, 16), make([]byte, 16), make([]byte, 20)
	c, err := NewCBC(key, iv, macKey, key, iv, macKey, sha1.New)
	if err != nil {
		t.Fatal(err)
	}
	header := recordlayer.Header{ContentType: protocol.ContentTypeApplicationData, Version: protocol.Version1_2, Epoch: 1}
	mac, err := c.hmac(header.Epoch, header.SequenceNumber, header.ContentType, header.Version, nil, macKey, sha1.New)
	if err != nil {
		t.Fatal(err)
	}
	valid := append([]byte{}, mac...)
	for len(valid) < 32 {
		valid = append(valid, 11)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
		valid  bool
	}{
		{"empty application record", func(p []byte) []byte { return p }, true},
		{"bad MAC", func(p []byte) []byte { p[0] ^= 1; return p }, false},
		{"nonuniform padding", func(p []byte) []byte { p[20] ^= 1; return p }, false},
		{"padding overlaps MAC", func(p []byte) []byte {
			for i := range p {
				p[i] = 31
			}
			return p
		}, false},
		{"oversized padding", func(p []byte) []byte { p[31] = 255; return p }, false},
		{"short ciphertext", func(p []byte) []byte { return p[:16] }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plaintext := tc.mutate(append([]byte{}, valid...))
			block, err := aes.NewCipher(key)
			if err != nil {
				t.Fatal(err)
			}
			ciphertext := make([]byte, len(plaintext))
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, plaintext)
			header.ContentLen = uint16(len(iv) + len(ciphertext))
			raw, err := header.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, iv...)
			raw = append(raw, ciphertext...)
			got, err := c.Decrypt(header, raw)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != header.Size() {
					t.Fatalf("unexpected plaintext length %d", len(got))
				}
			} else if err == nil {
				t.Fatalf("accepted malformed record: %x", got)
			}
		})
	}
}
