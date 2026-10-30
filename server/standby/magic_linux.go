package standby

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// MagicPacket accepts the standard synchronization sequence followed by 16
// copies of this NIC's six-byte MAC. A SecureOn suffix is not authentication.
func MagicPacket(packet, mac []byte) bool {
	if len(mac) != 6 {
		return false
	}
	pattern := append(bytes.Repeat([]byte{255}, 6), bytes.Repeat(mac, 16)...)
	return bytes.Contains(packet, pattern)
}

// ListenMagic opens all sockets before readiness. Only this physical interface
// is allowed; routed UDP depends on the network's broadcast/forwarding policy.
func ListenMagic(c Config, wake func(time.Time), failed func(error)) (func(), error) {
	nic, err := net.InterfaceByName(c.Interface)
	if err != nil {
		return nil, err
	}
	mac, err := net.ParseMAC(c.MAC)
	if err != nil || len(mac) != 6 {
		return nil, fmt.Errorf("invalid wake MAC")
	}
	var closeFns []func()
	closeAll := func() {
		for _, f := range closeFns {
			f()
		}
	}
	for _, port := range []int{7, 9} {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err != nil {
			closeAll()
			return nil, err
		}
		raw, err := conn.SyscallConn()
		if err == nil {
			var bindErr error
			err = raw.Control(func(fd uintptr) {
				bindErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, c.Interface)
				if bindErr == nil {
					bindErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TIMESTAMPNS, 1)
				}
			})
			if err == nil {
				err = bindErr
			}
		}
		if err != nil {
			conn.Close()
			closeAll()
			return nil, err
		}
		closeFns = append(closeFns, func() { conn.Close() })
		go func() {
			b := make([]byte, 2048)
			oob := make([]byte, 128)
			for {
				n, oobn, _, _, err := conn.ReadMsgUDP(b, oob)
				if err != nil {
					failed(err)
					return
				}
				if MagicPacket(b[:n], mac) {
					wake(packetTime(oob[:oobn]))
				}
			}
		}()
	}
	// SOCK_DGRAM strips the Ethernet header; ETH_P_WOL is distinct from HTTP
	// and UDP encapsulation. The SoC and NIC remain fully powered.
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, int(htons(0x0842)))
	if err != nil {
		closeAll()
		return nil, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TIMESTAMPNS, 1); err != nil {
		unix.Close(fd)
		closeAll()
		return nil, err
	}
	if err = unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(0x0842), Ifindex: nic.Index}); err != nil {
		unix.Close(fd)
		closeAll()
		return nil, err
	}
	closeFns = append(closeFns, func() { unix.Close(fd) })
	go func() {
		b := make([]byte, 2048)
		oob := make([]byte, 128)
		for {
			n, oobn, _, _, err := unix.Recvmsg(fd, b, oob, 0)
			if err != nil {
				failed(err)
				return
			}
			if MagicPacket(b[:n], mac) {
				wake(packetTime(oob[:oobn]))
			}
		}
	}()
	return closeAll, nil
}
func htons(n uint16) uint16 { return n<<8 | n>>8 }

// Kernel receive time closes the queued-before-ready race on both listeners.
// NanoKVM and host tests use a 64-bit Linux time_t.
func packetTime(oob []byte) time.Time {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return time.Time{}
	}
	for _, m := range messages {
		if m.Header.Level == unix.SOL_SOCKET && m.Header.Type == unix.SCM_TIMESTAMPNS && len(m.Data) >= 16 {
			sec := int64(binary.NativeEndian.Uint64(m.Data[:8]))
			nsec := int64(binary.NativeEndian.Uint64(m.Data[8:16]))
			if sec > 0 && nsec >= 0 && nsec < 1e9 {
				return time.Unix(sec, nsec)
			}
		}
	}
	return time.Time{}
}
