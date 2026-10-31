package hwcrypto

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"sync"
	"sync/atomic"
	"syscall"
)

// SHA-256 on the SG2002 crypto engine, through the sg2002-aes module's
// SG2002_CRYPTO_RUN ioctl: whole 64-byte blocks, at most shaChunk bytes per
// request, the big-endian chaining state in and out. On a NanoKVM (MB/s,
// crypto/sha256 with the C906 block against the engine): 4 KiB 23 against
// 110, 1 MiB 22 against 149, at a tenth of the CPU time. Below 1 KiB the
// request costs more than it saves.
const shaChunk = 4096

// shaEngine runs whole blocks on the engine, updating state.
type shaEngine struct {
	run      func(state *[32]byte, blocks []byte) error
	disabled atomic.Bool
}

var (
	shaEngineOnce sync.Once
	shaEngineDev  *shaEngine
)

// NewSHA256 returns a SHA-256 hash for bulk data (downloads, update
// packages). A message of at most 4 KiB never reaches the engine: it is hashed
// in software at Sum. Larger ones go to the engine 4 KiB at a time, and the
// last partial chunk and the padding are hashed in software. If the engine
// fails, the hash continues in software from the same state and the engine is
// not used again. Without the device this is crypto/sha256.
func NewSHA256() hash.Hash {
	shaEngineOnce.Do(func() { shaEngineDev = openSHAEngine() })
	if shaEngineDev == nil || shaEngineDev.disabled.Load() {
		return sha256.New()
	}
	return newSHA256(shaEngineDev)
}

type sha256Hybrid struct {
	engine *shaEngine
	h      [8]uint32
	buf    [shaChunk]byte
	n      int    // bytes in buf
	total  uint64 // bytes written
}

func newSHA256(e *shaEngine) *sha256Hybrid {
	d := &sha256Hybrid{engine: e}
	d.Reset()
	return d
}

func (d *sha256Hybrid) Size() int      { return sha256.Size }
func (d *sha256Hybrid) BlockSize() int { return sha256.BlockSize }

func (d *sha256Hybrid) Reset() {
	d.h = [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
		0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}
	d.n, d.total = 0, 0
}

// Write keeps the last chunk buffered until more data arrives, so that a
// message of exactly 4 KiB stays in software.
func (d *sha256Hybrid) Write(p []byte) (int, error) {
	n := len(p)
	d.total += uint64(n)
	for len(p) > 0 {
		if d.n == 0 && len(p) > shaChunk {
			d.blocks(p[:shaChunk])
			p = p[shaChunk:]
			continue
		}
		c := copy(d.buf[d.n:], p)
		d.n += c
		p = p[c:]
		if d.n == shaChunk && len(p) > 0 {
			d.blocks(d.buf[:])
			d.n = 0
		}
	}
	return n, nil
}

// blocks hashes whole blocks on the engine, or in software if it fails.
func (d *sha256Hybrid) blocks(p []byte) {
	e := d.engine
	if e != nil && !e.disabled.Load() {
		var state [32]byte
		for i, v := range d.h {
			binary.BigEndian.PutUint32(state[i*4:], v)
		}
		err := e.run(&state, p)
		if err == nil {
			for i := range d.h {
				d.h[i] = binary.BigEndian.Uint32(state[i*4:])
			}
			return
		}
		// The module takes its lock interruptibly and submits nothing when a
		// signal arrives first: this chunk uses software, the engine stays.
		if !errors.Is(err, syscall.EINTR) {
			e.disabled.Store(true)
		}
	}
	blockGeneric(&d.h, p)
}

// Sum finishes in software on a copy of the state.
func (d *sha256Hybrid) Sum(b []byte) []byte {
	if d.total <= shaChunk {
		s := sha256.Sum256(d.buf[:d.n])
		return append(b, s[:]...)
	}
	h := d.h
	full := d.n &^ 63
	blockGeneric(&h, d.buf[:full])
	var tail [128]byte
	t := copy(tail[:], d.buf[full:d.n])
	tail[t] = 0x80
	end := 64
	if t >= 56 {
		end = 128
	}
	binary.BigEndian.PutUint64(tail[end-8:], d.total<<3)
	blockGeneric(&h, tail[:end])
	for _, v := range h {
		b = binary.BigEndian.AppendUint32(b, v)
	}
	return b
}
