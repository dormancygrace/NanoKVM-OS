// Package sg2002aes adapts the Enhanced SG2002 AES-CTR miscdevice to Pion SRTP.
package sg2002aes

import (
	"errors"
	"runtime"
	"sync"
)

const MaxPayload = 4096

// Small payloads use software to avoid fixed ioctl/DMA costs. This conservative
// initial threshold is not a measured crossover for all packet sizes.
const MinPayload = 512

var ErrUnavailable = errors.New("SG2002 AES device unavailable on this platform")
var errCompletion = errors.New("SG2002 AES returned an invalid completion status")

// Keep this ABI in sync with firmware/crypto/experimental/sg2002-aes-probe.
type request struct {
	Length, Reserved uint32
	Key, IV          [16]byte
	Status           uint32
	Data             [MaxPayload]byte
}

// Device serializes access to one reusable request buffer. No packet/key data
// are exposed in errors. The first device failure permanently disables this
// instance; the kernel may have pinned DMA buffers pending a reboot.
type Device struct {
	mu       sync.Mutex
	req      request
	submit   func(*request) error
	closeFD  func() error
	onError  func(error)
	disabled bool
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
	if err == nil {
		copy(dst, r.Data[:len(src)])
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
	return err == nil
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
