//go:build linux

package udpbatch

import (
	"encoding/binary"
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// All diagnostic counters are accessed under Conn.mu.
type gsoDiagnostics struct {
	attempts, bundles, datagrams, fallbacks uint64
}

// UDP_SEGMENT describes full-size datagrams followed by one optional short
// datagram. Other shapes use the ordinary sendmmsg path unchanged.
func (c *Conn) sendGSOLocked(count int) (int, error, bool) {
	if c.gsoBuffer == nil || count < 2 {
		return 0, nil, false
	}
	segment := c.slots[0].n
	if segment <= 0 {
		return 0, nil, false
	}
	total := 0
	for i := 0; i < count; i++ {
		n := c.slots[i].n
		if n <= 0 || n > segment || (i != count-1 && n != segment) {
			return 0, nil, false
		}
		total += n
	}
	if total > len(c.gsoBuffer) {
		return 0, nil, false
	}
	buffer := c.gsoBuffer
	offset := 0
	for i := 0; i < count; i++ {
		offset += copy(buffer[offset:], c.slots[i].data[:c.slots[i].n])
	}
	defer clear(buffer[:total])

	var messages [maxBatchPackets]mmsghdr
	iov := unix.Iovec{Base: &buffer[0], Len: uint64(total)}
	messages[0].msg.Iov = &iov
	messages[0].msg.Iovlen = 1
	var addr4 unix.RawSockaddrInet4
	var addr6 unix.RawSockaddrInet6
	address := c.slots[0].addr
	if c.family == unix.AF_INET {
		addr4.Family = unix.AF_INET
		binary.BigEndian.PutUint16((*[2]byte)(unsafe.Pointer(&addr4.Port))[:], address.Port())
		addr4.Addr = address.Addr().As4()
		messages[0].msg.Name = (*byte)(unsafe.Pointer(&addr4))
		messages[0].msg.Namelen = uint32(unsafe.Sizeof(addr4))
	} else {
		addr6.Family = unix.AF_INET6
		binary.BigEndian.PutUint16((*[2]byte)(unsafe.Pointer(&addr6.Port))[:], address.Port())
		addr6.Addr = address.Addr().As16()
		messages[0].msg.Name = (*byte)(unsafe.Pointer(&addr6))
		messages[0].msg.Namelen = uint32(unsafe.Sizeof(addr6))
	}
	var control [24]byte // CMSG_SPACE(sizeof(uint16)) on supported 64-bit Linux.
	header := (*unix.Cmsghdr)(unsafe.Pointer(&control[0]))
	header.Level = unix.SOL_UDP
	header.Type = unix.UDP_SEGMENT
	header.SetLen(unix.CmsgLen(2))
	binary.NativeEndian.PutUint16(control[unix.CmsgLen(0):], uint16(segment))
	messages[0].msg.Control = &control[0]
	messages[0].msg.SetControllen(unix.CmsgSpace(2))

	if c.gsoStats != nil {
		c.gsoStats.attempts++
	}
	sent, err := c.sendMessagesLocked(&messages, 1)
	if sent == 0 && gsoUnsupported(err) {
		// An unsuccessful atomic UDP send accepted no datagrams. Disable this
		// optional path for the socket and retry through ordinary sendmmsg.
		c.gsoBuffer = nil
		if c.gsoStats != nil {
			c.gsoStats.fallbacks++
		}
		return 0, nil, false
	}
	if sent != 0 {
		if c.gsoStats != nil {
			c.gsoStats.bundles++
			c.gsoStats.datagrams += uint64(count)
		}
		return count, err, true
	}
	return 0, err, true
}

func gsoUnsupported(err error) bool {
	// Retry only errors that identify an unavailable UDP_SEGMENT path.
	// EMSGSIZE and EIO may describe a packet or transport failure; treating
	// either as capability failure could resend an already accepted datagram.
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EOPNOTSUPP) ||
		errors.Is(err, syscall.ENOPROTOOPT) || errors.Is(err, syscall.ENOSYS)
}
