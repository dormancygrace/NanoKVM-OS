// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package ciphersuite

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"

	"github.com/pion/dtls/v3/pkg/protocol/recordlayer"
)

const (
	gcmTagLength   = 16
	gcmNonceLength = 12
)

// GCM Provides an API to Encrypt/Decrypt DTLS 1.2 Packets.
type GCM struct {
	aead *aead

	// For sealing records in batches outside this package: the local key and its hash key E_K(0).
	localKey []byte
	hashKey  [16]byte
}

// NewGCM creates a DTLS GCM Cipher.
func NewGCM(localKey, localWriteIV, remoteKey, remoteWriteIV []byte) (*GCM, error) {
	localBlock, err := aes.NewCipher(localKey)
	if err != nil {
		return nil, err
	}
	localGCM, err := cipher.NewGCM(localBlock)
	if err != nil {
		return nil, err
	}

	remoteBlock, err := aes.NewCipher(remoteKey)
	if err != nil {
		return nil, err
	}
	remoteGCM, err := cipher.NewGCM(remoteBlock)
	if err != nil {
		return nil, err
	}

	gcm := &GCM{
		aead: newAEAD(
			localGCM,
			localWriteIV,
			remoteGCM,
			remoteWriteIV,
			gcmNonceLength,
			gcmTagLength,
		),
		localKey: append([]byte{}, localKey...),
	}
	localBlock.Encrypt(gcm.hashKey[:], gcm.hashKey[:])

	return gcm, nil
}

// BatchKey returns the key the local records are sealed with and its hash key E_K(0), for a function that
// seals the records of AppendPlainApplicationData.
func (g *GCM) BatchKey() (key []byte, hashKey *[16]byte) {
	return g.localKey, &g.hashKey
}

// ApplicationDataSize is the length of the record of an application-data payload.
func (g *GCM) ApplicationDataSize(payload int) int {
	return recordlayer.FixedHeaderSize + 8 + payload + gcmTagLength
}

// AppendPlainApplicationData appends to dst the record SealApplicationData makes of the payload, with the
// payload not yet encrypted and 16 zero bytes where the tag goes. It returns where the payload is in the result
// and fills in what sealing it needs: the counter block J0 (the nonce, then 00 00 00 01) and the 13 bytes of
// associated data in aad.
func (g *GCM) AppendPlainApplicationData(
	dst []byte, epoch uint16, sequenceNumber uint64, payload []byte, j0, aad *[16]byte,
) (out []byte, text int) {
	seq64 := (uint64(epoch) << 48) | (sequenceNumber & 0x0000ffffffffffff)
	copy(j0[:4], g.aead.localWriteIV[:4])
	binary.BigEndian.PutUint64(j0[4:], seq64)
	j0[12], j0[13], j0[14], j0[15] = 0, 0, 0, 1

	var additionalData [13]byte
	dst = applicationDataRecord(dst, &additionalData, seq64, 8+len(payload)+gcmTagLength, len(payload))
	*aad = [16]byte{}
	copy(aad[:], additionalData[:])
	dst = append(dst, j0[4:12]...)
	text = len(dst)
	dst = append(dst, payload...)

	return append(dst, make([]byte, gcmTagLength)...), text
}

// Encrypt encrypts a DTLS RecordLayer message.
func (g *GCM) Encrypt(pkt *recordlayer.RecordLayer, raw []byte) ([]byte, error) {
	return g.aead.encrypt(pkt, raw)
}

// SealApplicationData appends to dst the encrypted record of one application-data payload (DTLS 1.2, no
// connection ID): the bytes Encrypt returns for the marshaled record. dst must not overlap payload.
func (g *GCM) SealApplicationData(dst []byte, epoch uint16, sequenceNumber uint64, payload []byte) []byte {
	return g.aead.sealApplicationData(dst, epoch, sequenceNumber, payload)
}

// Decrypt decrypts a DTLS RecordLayer message.
func (g *GCM) Decrypt(header recordlayer.Header, in []byte) ([]byte, error) {
	return g.aead.decrypt(header, in)
}
