//go:build linux

package utils

import (
	"errors"
	"net"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ingressIndex is the interface index the connection's SYN arrived on. The
// kernel records it when it creates the connection (mc_index for IPv4,
// mcast_oif for IPv6 and IPv4-mapped peers) and reports it through the RFC
// 2292 packet options; IP_PKTINFO and IPV6_RECVPKTINFO only enable that
// report, so they can be set after accept.
func ingressIndex(conn *net.TCPConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var index int
	var indexErr error
	if err := raw.Control(func(fd uintptr) { index, indexErr = ingressIndexFD(int(fd)) }); err != nil {
		return 0, err
	}
	return index, indexErr
}

func ingressIndexFD(fd int) (int, error) {
	family, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_DOMAIN)
	if err != nil {
		return 0, err
	}
	level, enable, options, report := unix.IPPROTO_IP, unix.IP_PKTINFO, unix.IP_PKTOPTIONS, unix.IP_PKTINFO
	if family == unix.AF_INET6 {
		level, enable, options, report = unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO, unix.IPV6_2292PKTOPTIONS, unix.IPV6_PKTINFO
	}
	if err := unix.SetsockoptInt(fd, level, enable, 1); err != nil {
		return 0, err
	}
	buf := make([]byte, 512)
	size := uint32(len(buf))
	// No x/sys wrapper returns raw option bytes: GetsockoptString stops at
	// the first zero byte.
	if _, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), uintptr(level), uintptr(options),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); errno != 0 {
		return 0, errno
	}
	messages, err := unix.ParseSocketControlMessage(buf[:size])
	if err != nil {
		return 0, err
	}
	for _, m := range messages {
		if m.Header.Level != int32(level) || m.Header.Type != int32(report) {
			continue
		}
		var index int
		if family == unix.AF_INET6 && len(m.Data) >= unix.SizeofInet6Pktinfo {
			index = int((*unix.Inet6Pktinfo)(unsafe.Pointer(&m.Data[0])).Ifindex)
		} else if family == unix.AF_INET && len(m.Data) >= unix.SizeofInet4Pktinfo {
			index = int((*unix.Inet4Pktinfo)(unsafe.Pointer(&m.Data[0])).Ifindex)
		}
		if index > 0 {
			return index, nil
		}
	}
	return 0, errors.New("the kernel reported no ingress interface")
}
