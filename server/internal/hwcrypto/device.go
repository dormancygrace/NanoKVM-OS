// Package hwcrypto adapts the SG2002 AES-CTR miscdevice to Pion SRTP.
package hwcrypto

import (
	"errors"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const MaxPayload = 4096

// Smaller payloads use software: the fixed ioctl and request copy costs win
// below this. Measured on the C906 (commit a0fe2a62), CPU time per protected
// RTP packet (software against hardware): 192 bytes 54.0 against 55.1 us, 256 bytes 65.9
// against 58.5 us, 384 bytes 88.1 against 69.9 us, 1188 bytes 231 against
// 125 us. Audio (Opus, about 100-200 bytes) stays in software.
const MinPayload = 256

var ErrUnavailable = errors.New("SG2002 AES device unavailable on this platform")
var errCompletion = errors.New("SG2002 AES returned an invalid completion status")

// Keep this ABI in sync with platform/modules/hwcrypto.
type request struct {
	Length, Reserved uint32
	Key, IV          [16]byte
	Status           uint32
	Data             [MaxPayload]byte
}

// MaxCTRSHA1 is the largest batch of Device.CTRSHA1 (HWCRYPTO_CTR_SHA1_MAX).
const MaxCTRSHA1 = 64

// CTRSHA1Packet is one message of Device.CTRSHA1: Data is hashed whole (its
// length a multiple of 64, at most MaxPayload) after CryptLength bytes (a
// nonzero multiple of 16) at CryptOffset (a multiple of 4) were encrypted in
// place with IV as the first counter block. State returns the SHA-1 state
// after Data.
type CTRSHA1Packet struct {
	Data        []byte
	CryptOffset int
	CryptLength int
	IV          [16]byte
	State       [20]byte
}

// Keep these in sync with platform/modules/hwcrypto/hwcrypto.h.
type ctrSHA1Packet struct {
	data                                       uint64
	length, cryptOffset, cryptLength, reserved uint32
	iv                                         [16]byte
	state                                      [20]byte
	reserved2                                  uint32
}

type ctrSHA1Request struct {
	version, count uint32
	key            [16]byte
	state          [20]byte
	reserved       uint32
	packets        uint64
	reserved2      [4]uint32
}

// Device serializes access to one reusable request buffer. No packet/key data
// are exposed in errors. The first device failure permanently disables this
// instance; the kernel may have pinned DMA buffers pending a reboot.
type Device struct {
	mu     sync.Mutex
	req    request
	submit func(*request) error
	// submitChain runs HWCRYPTO_CTR_SHA1; noChain is set when the loaded
	// module does not know it (an application update before the reboot into
	// its kernel modules).
	submitChain func(*ctrSHA1Request) error
	chain       [MaxCTRSHA1]ctrSHA1Packet
	chainReq    ctrSHA1Request
	noChain     bool
	closeFD     func() error
	onError     func(error)
	disabled    bool
}

// TryXORKeyStream implements srtp.AESCTRAccelerator. Hardware writes only to the
// private request buffer, so errors preserve the caller's input even in place.
func (d *Device) TryXORKeyStream(key, iv, dst, src []byte) bool {
	if len(key) != 16 || len(iv) != 16 || len(src) < MinPayload || len(src) > MaxPayload || len(dst) < len(src) {
		return false
	}
	d.mu.Lock()
	if d.disabled {
		d.mu.Unlock()
		return false
	}
	r := &d.req
	r.Length, r.Reserved, r.Status = uint32(len(src)), 0, 0
	copy(r.Key[:], key)
	copy(r.IV[:], iv)
	copy(r.Data[:], src)
	err := d.submit(r)
	if err == nil && r.Status != 1 {
		err = errCompletion
	}
	interrupted := errors.Is(err, syscall.EINTR)
	if err == nil {
		copy(dst, r.Data[:len(src)])
	} else if interrupted {
		// The module takes its lock interruptibly and submits nothing when a
		// signal arrives first: this packet uses software, the device stays.
		err = nil
	} else {
		d.disabled = true
		if d.closeFD != nil {
			_ = d.closeFD()
			d.closeFD = nil
		}
	}
	clear(r.Key[:])
	clear(r.IV[:])
	clear(r.Data[:len(src)])
	runtime.KeepAlive(r)
	onError := d.onError
	d.mu.Unlock()
	if err != nil && onError != nil {
		onError(err)
	}
	return err == nil && !interrupted
}

// CTRSHA1 runs AES-128-CTR and then SHA-1 from state over each packet on the
// engine, one chain of two descriptors per packet (HWCRYPTO_CTR_SHA1), and
// returns how many packets, from the first, it completed. The packets after
// those are untouched. It completes none when the device is disabled, the
// module does not have the request or a signal arrived first.
func (d *Device) CTRSHA1(key *[16]byte, state *[20]byte, packets []CTRSHA1Packet) int {
	if len(packets) == 0 || len(packets) > MaxCTRSHA1 {
		return 0
	}
	d.mu.Lock()
	if d.disabled || d.noChain || d.submitChain == nil {
		d.mu.Unlock()
		return 0
	}
	for i := range packets {
		p := &packets[i]
		d.chain[i] = ctrSHA1Packet{
			data:   uint64(uintptr(unsafe.Pointer(unsafe.SliceData(p.Data)))),
			length: uint32(len(p.Data)), cryptOffset: uint32(p.CryptOffset), cryptLength: uint32(p.CryptLength),
			iv: p.IV,
		}
	}
	r := &d.chainReq
	*r = ctrSHA1Request{version: 1, count: uint32(len(packets)), key: *key, state: *state,
		packets: uint64(uintptr(unsafe.Pointer(&d.chain[0])))}
	err := d.submitChain(r)
	runtime.KeepAlive(packets)
	done := 0
	switch {
	case err == nil:
		done = min(int(r.count), len(packets))
		for i := range done {
			packets[i].State = d.chain[i].state
		}
	case errors.Is(err, syscall.ENOTTY):
		d.noChain = true
		err = nil
	case errors.Is(err, syscall.EINTR):
		err = nil
	default:
		d.disabled = true
		if d.closeFD != nil {
			_ = d.closeFD()
			d.closeFD = nil
		}
	}
	clear(r.key[:])
	clear(d.chain[:len(packets)])
	onError := d.onError
	d.mu.Unlock()
	if err != nil && onError != nil {
		onError(err)
	}
	return done
}

// Close releases the process's descriptor and disables further requests.
// It never unloads or resets the kernel module.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.disabled = true
	if d.closeFD == nil {
		return nil
	}
	err := d.closeFD()
	d.closeFD = nil
	return err
}
