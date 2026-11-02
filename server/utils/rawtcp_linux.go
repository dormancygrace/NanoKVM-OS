//go:build linux

package utils

import (
	"io"
	"net"
	"os"
	"syscall"
	"unsafe"
)

// RawListener accepts TCP connections whose reads and writes are raw
// syscalls behind the netpoller. Each os/net read or write enters the
// runtime's syscall state; with the only P idle between video frames sysmon
// sleeps deeply and entersyscall wakes it, a futex wake and a sysmon run for
// every frame a viewer receives. The sockets are non-blocking, so a raw call
// returns EAGAIN instead of blocking and the netpoller waits as before,
// deadlines included.
func RawListener(l net.Listener) net.Listener { return rawListener{l} }

type rawListener struct{ net.Listener }

func (l rawListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return conn, nil
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return conn, nil
	}
	return &rawTCPConn{TCPConn: tcp, raw: raw}, nil
}

type rawTCPConn struct {
	*net.TCPConn
	raw syscall.RawConn
}

// TCP returns the accepted connection, for socket queries.
func (c *rawTCPConn) TCP() *net.TCPConn { return c.TCPConn }

func (c *rawTCPConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	var n int
	var errno syscall.Errno
	err := c.raw.Read(func(fd uintptr) bool {
		for {
			r, _, e := syscall.RawSyscall(syscall.SYS_READ, fd, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
			switch e {
			case syscall.EINTR:
				continue
			case syscall.EAGAIN:
				return false
			}
			n, errno = int(r), e
			return true
		}
	})
	if err != nil {
		return 0, c.opError("read", err)
	}
	if errno != 0 {
		return 0, c.opError("read", os.NewSyscallError("read", errno))
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// Write sends with MSG_NOSIGNAL: a peer that has gone away gives EPIPE, as
// the net package's writes do, without a SIGPIPE.
func (c *rawTCPConn) Write(b []byte) (int, error) {
	written := 0
	var errno syscall.Errno
	stalled := false
	err := c.raw.Write(func(fd uintptr) bool {
		for written < len(b) {
			r, _, e := syscall.RawSyscall6(syscall.SYS_SENDTO, fd, uintptr(unsafe.Pointer(&b[written])), uintptr(len(b)-written),
				syscall.MSG_NOSIGNAL, 0, 0)
			switch e {
			case 0:
				if r == 0 { // as internal/poll: no progress is an error, not a spin
					stalled = true
					return true
				}
				written += int(r)
			case syscall.EINTR:
			case syscall.EAGAIN:
				return false
			default:
				errno = e
				return true
			}
		}
		return true
	})
	if err != nil {
		return written, c.opError("write", err)
	}
	if errno != 0 {
		return written, c.opError("write", os.NewSyscallError("write", errno))
	}
	if stalled {
		return written, c.opError("write", io.ErrUnexpectedEOF)
	}
	return written, nil
}

// opError reports like the net package; RawConn's own errors (deadline,
// closed) already are *net.OpError.
func (c *rawTCPConn) opError(op string, err error) error {
	if _, ok := err.(*net.OpError); ok {
		return err
	}
	return &net.OpError{Op: op, Net: "tcp", Source: c.LocalAddr(), Addr: c.RemoteAddr(), Err: err}
}
