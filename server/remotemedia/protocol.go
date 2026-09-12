// Package remotemedia serves read-only browser-backed block media.
package remotemedia

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	requestMagic        = 0x25609513
	replyMagic          = 0x67446698
	MaxRead             = 1024 * 1024
	MaxImageSize uint64 = 512 * 1024 * 1024 * 1024
)

type ReadBlock func(offset uint64, length uint32) ([]byte, error)

// Serve handles the transmission phase of the standard Linux NBD protocol.
// The ioctl attachment supplies size/read-only flags; there is no network-facing
// NBD listener or negotiation endpoint. Only one bounded read is in flight.
func Serve(rw io.ReadWriter, size uint64, read ReadBlock) error {
	var request [28]byte
	for {
		if _, err := io.ReadFull(rw, request[:]); err != nil {
			return err
		}
		if binary.BigEndian.Uint32(request[:4]) != requestMagic {
			return errors.New("invalid NBD request magic")
		}
		command := binary.BigEndian.Uint32(request[4:8])
		offset := binary.BigEndian.Uint64(request[16:24])
		length := binary.BigEndian.Uint32(request[24:28])
		if length > MaxRead {
			return errors.New("NBD request exceeds memory limit")
		}
		if command == 2 {
			return nil
		} // NBD_CMD_DISC has no reply.
		var errno uint32
		var data []byte
		switch command & 0xffff {
		case 0: // NBD_CMD_READ
			if command != 0 || offset > size || uint64(length) > size-offset {
				errno = 22 // EINVAL; subtraction avoids offset overflow.
			} else if length != 0 {
				var err error
				data, err = read(offset, length)
				if err != nil || len(data) != int(length) {
					errno = 5
					data = nil
				}
			}
		case 1: // Consume a rejected write so subsequent request framing is intact.
			if _, err := io.CopyN(io.Discard, rw, int64(length)); err != nil {
				return err
			}
			errno = 30 // EROFS
		default:
			errno = 95 // EOPNOTSUPP; no flush/trim/write-zeroes are advertised.
		}
		var reply [16]byte
		binary.BigEndian.PutUint32(reply[:4], replyMagic)
		binary.BigEndian.PutUint32(reply[4:8], errno)
		copy(reply[8:], request[8:16]) // The handle is opaque, not an integer.
		if err := writeAll(rw, reply[:]); err != nil {
			return err
		}
		if errno == 0 {
			if err := writeAll(rw, data); err != nil {
				return err
			}
		}
	}
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func ValidateImage(size uint64) error {
	if size < 32768 || size > MaxImageSize || size%2048 != 0 {
		return errors.New("ISO size must be 2048-byte aligned, between 32 KiB and 512 GiB")
	}
	return nil
}
