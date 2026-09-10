//go:build linux && (amd64 || arm64 || riscv64)

package udpfast

import (
	"encoding/binary"
	"github.com/pion/transport/v4"
	"net"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

type udpConn struct {
	*net.UDPConn
	raw    syscall.RawConn
	family int
}

// Wrap leaves unsupported or connected sockets on the standard implementation.
func Wrap(c *net.UDPConn) transport.UDPConn {
	if c.RemoteAddr() != nil {
		return c
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return c
	}
	family := 0
	controlErr := raw.Control(func(fd uintptr) { family, err = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_DOMAIN) })
	if controlErr != nil || err != nil || (family != syscall.AF_INET && family != syscall.AF_INET6) {
		return c
	}
	return &udpConn{UDPConn: c, raw: raw, family: family}
}

func (c *udpConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if udp, ok := addr.(*net.UDPAddr); ok {
		return c.WriteToUDP(b, udp)
	}
	return c.UDPConn.WriteTo(b, addr)
}

func (c *udpConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	if addr == nil || addr.Port < 0 || addr.Port > 65535 || addr.Zone != "" {
		return c.UDPConn.WriteToUDP(b, addr)
	}
	var a4 syscall.RawSockaddrInet4
	var a6 syscall.RawSockaddrInet6
	var dest unsafe.Pointer
	var size uintptr
	if c.family == syscall.AF_INET {
		ip := addr.IP.To4()
		if ip == nil {
			return c.UDPConn.WriteToUDP(b, addr)
		}
		a4.Family = syscall.AF_INET
		copy(a4.Addr[:], ip)
		binary.BigEndian.PutUint16((*[2]byte)(unsafe.Pointer(&a4.Port))[:], uint16(addr.Port))
		dest = unsafe.Pointer(&a4)
		size = unsafe.Sizeof(a4)
	} else {
		ip := addr.IP.To16()
		if ip == nil {
			return c.UDPConn.WriteToUDP(b, addr)
		}
		a6.Family = syscall.AF_INET6
		copy(a6.Addr[:], ip)
		binary.BigEndian.PutUint16((*[2]byte)(unsafe.Pointer(&a6.Port))[:], uint16(addr.Port))
		dest = unsafe.Pointer(&a6)
		size = unsafe.Sizeof(a6)
	}
	var data unsafe.Pointer
	if len(b) > 0 {
		data = unsafe.Pointer(&b[0])
	}
	written := 0
	var sendErr syscall.Errno
	// RawConn.Write retains the standard write lock, deadline check, poll wait,
	// and close coordination. Only the nonblocking syscall bypasses entersyscall.
	err := c.raw.Write(func(fd uintptr) bool {
		for {
			n, _, errno := syscall.RawSyscall6(syscall.SYS_SENDTO, fd, uintptr(data), uintptr(len(b)), syscall.MSG_DONTWAIT, uintptr(dest), size)
			if errno == syscall.EINTR {
				continue
			}
			if errno == syscall.EAGAIN || errno == syscall.EWOULDBLOCK {
				return false
			}
			sendErr = errno
			if errno == 0 {
				written = int(n)
			}
			return true
		}
	})
	runtime.KeepAlive(b)
	runtime.KeepAlive(dest)
	runtime.KeepAlive(c)
	if err == nil && sendErr != 0 {
		err = os.NewSyscallError("sendto", sendErr)
	}
	if err != nil {
		return written, &net.OpError{Op: "write", Net: c.LocalAddr().Network(), Source: c.LocalAddr(), Addr: addr, Err: err}
	}
	return written, nil
}

var _ transport.UDPConn = (*udpConn)(nil)
