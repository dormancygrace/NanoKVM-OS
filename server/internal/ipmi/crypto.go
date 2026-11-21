package ipmi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"hash"
)

// Algorithms of the two cipher suites served: 3 (RAKP-HMAC-SHA1,
// HMAC-SHA1-96, AES-CBC-128), ipmitool's default before 1.8.19, and 17
// (RAKP-HMAC-SHA256, HMAC-SHA256-128, AES-CBC-128), its default since.
// Suites without authentication, integrity or confidentiality are refused.
const (
	authHMACSHA1   = 0x01
	authHMACSHA256 = 0x03

	integrityHMACSHA1_96   = 0x01
	integrityHMACSHA256128 = 0x04

	confAESCBC128 = 0x01
)

type suite struct {
	id                 byte
	auth, integrity    byte
	newHash            func() hash.Hash
	rakp4Len, integLen int
}

var suites = []suite{
	{id: 3, auth: authHMACSHA1, integrity: integrityHMACSHA1_96, newHash: sha1.New, rakp4Len: 12, integLen: 12},
	{id: 17, auth: authHMACSHA256, integrity: integrityHMACSHA256128, newHash: sha256.New, rakp4Len: 16, integLen: 16},
}

func suiteFor(auth, integrity, conf byte) (suite, bool) {
	for _, s := range suites {
		if s.auth == auth && s.integrity == integrity && conf == confAESCBC128 {
			return s, true
		}
	}
	return suite{}, false
}

func (s suite) mac(key []byte, parts ...[]byte) []byte {
	m := hmac.New(s.newHash, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// keys derives the session keys from SIK: K1 signs the packets, the first
// 16 bytes of K2 encrypt them (IPMI v2.0, 13.32).
// The constants are 20 bytes also with SHA-256, as ipmitool and BMCs use
// them, not the hash size.
func (s suite) keys(sik []byte) (k1, k2 []byte) {
	one, two := make([]byte, 20), make([]byte, 20)
	for i := range one {
		one[i], two[i] = 0x01, 0x02
	}
	return s.mac(sik, one), s.mac(sik, two)
}

// encrypt pads the payload as IPMI v2.0, 13.29 requires (bytes 1, 2, 3... and
// the pad length) and prepends a random IV.
func encrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return nil, err
	}
	padLen := (aes.BlockSize - (len(plain)+1)%aes.BlockSize) % aes.BlockSize
	data := make([]byte, 0, len(plain)+padLen+1)
	data = append(data, plain...)
	for i := 1; i <= padLen; i++ {
		data = append(data, byte(i))
	}
	data = append(data, byte(padLen))
	out := make([]byte, aes.BlockSize+len(data))
	if _, err := rand.Read(out[:aes.BlockSize]); err != nil {
		return nil, err
	}
	cipher.NewCBCEncrypter(block, out[:aes.BlockSize]).CryptBlocks(out[aes.BlockSize:], data)
	return out, nil
}

func decrypt(key, data []byte) ([]byte, error) {
	if len(data) < 2*aes.BlockSize || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("bad encrypted payload length")
	}
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(data)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, data[:aes.BlockSize]).CryptBlocks(plain, data[aes.BlockSize:])
	padLen := int(plain[len(plain)-1])
	if padLen >= aes.BlockSize || padLen+1 > len(plain) {
		return nil, errors.New("bad confidentiality pad")
	}
	return plain[:len(plain)-1-padLen], nil
}
