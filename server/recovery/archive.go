// Package recovery implements bounded, authenticated configuration archives.
// It never accepts an archive-selected destination outside its settings policy.
package recovery

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

const MaxArchive = 4 << 20
const MaxData = 2 << 20
const MaxFile = 512 << 10
const MaxFiles = 1024
const magic = "NKOSBACKUP1\n"
const iterations = 600000

type File struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
	Mode uint32 `json:"mode"`
}
type Archive struct {
	Format   int       `json:"format"`
	Version  string    `json:"version"`
	Board    string    `json:"board"`
	Created  time.Time `json:"created"`
	Files    []File    `json:"files"`
	Packages []string  `json:"packages"`
	RootHash string    `json:"rootHash,omitempty"`
}

func passwordOK(password string) error {
	if len(password) < 12 || len(password) > 256 {
		return errors.New("backup password must be 12–256 bytes")
	}
	return nil
}
func aead(password string, salt []byte) (cipher.AEAD, error) {
	key := pbkdf2.Key([]byte(password), salt, iterations, 32, sha256.New)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func Seal(a Archive, password string) ([]byte, error) {
	if err := passwordOK(password); err != nil {
		return nil, err
	}
	if err := Validate(a); err != nil {
		return nil, err
	}
	data, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return nil, err
	}
	g, err := aead(password, salt)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	header := append(append([]byte(magic), salt...), nonce...)
	if len(data)+len(header)+g.Overhead() > MaxArchive {
		return nil, errors.New("archive exceeds encrypted size limit")
	}
	return g.Seal(header, nonce, data, header), nil
}
func Open(data []byte, password string) (Archive, error) {
	var a Archive
	if err := passwordOK(password); err != nil {
		return a, err
	}
	n := len(magic) + 16 + 12
	if len(data) > MaxArchive || len(data) < n+16 || string(data[:len(magic)]) != magic {
		return a, errors.New("not a supported NanoKVM settings archive")
	}
	g, err := aead(password, data[len(magic):len(magic)+16])
	if err != nil {
		return a, err
	}
	plain, err := g.Open(nil, data[n-12:n], data[n:], data[:n])
	if err != nil {
		return a, errors.New("incorrect password or damaged archive")
	}
	defer clear(plain)
	if len(plain) > MaxArchive {
		return a, errors.New("archive too large")
	}
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&a); err != nil {
		return a, errors.New("invalid archive manifest")
	}
	if dec.Decode(new(any)) != io.EOF {
		return a, errors.New("trailing archive data")
	}
	return a, Validate(a)
}
