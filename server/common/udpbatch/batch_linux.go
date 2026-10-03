//go:build linux

// Package udpbatch provides a bounded, frame-scoped UDP sendmmsg adapter.
//
// The adapter deliberately does not change ordinary PacketConn semantics by
// queueing arbitrary writes. Only RTP media writes observed between BeginFrame
// and EndFrame are copied into bounded slots. EndFrame performs the actual
// send and returns its error to the caller.
package udpbatch

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"NanoKVM-Server/common/udpfast"
	"github.com/pion/transport/v5"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	maxBatchPackets      = 16
	maxPacketBytes       = 2048
	defaultFlushInterval = time.Millisecond
	batchFlushEnv        = "NANOKVM_WEBRTC_UDP_BATCH_FLUSH_US"
)

type packetSlot struct {
	data   [maxPacketBytes]byte
	n      int
	addr   netip.AddrPort
	marker bool
}

// Linux's struct mmsghdr is struct msghdr followed by an unsigned int and
// padding to the struct alignment. This shape is stable on the supported
// amd64, arm64, and riscv64 targets.
type mmsghdr struct {
	msg    unix.Msghdr
	length uint32
	_      uint32
}

type sendmmsgFunc func(fd uintptr, messages unsafe.Pointer, count uintptr) (uintptr, syscall.Errno)

type timerState struct {
	timer *time.Timer
}

// Conn is a frame-aware wrapper around one unconnected UDP socket.
type Conn struct {
	transport.UDPConn
	udp    *net.UDPConn
	raw    syscall.RawConn
	family int

	onClose   func()
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool

	mu            sync.Mutex
	frame         bool
	count         int
	addr          netip.AddrPort
	slots         [maxBatchPackets]packetSlot
	timerState    *timerState
	timerArmed    bool
	flushInterval time.Duration
	sticky        error
	sendmmsg      sendmmsgFunc
	gsoBuffer     *[maxBatchPackets * maxPacketBytes]byte
	gsoStats      *gsoDiagnostics
}

// Wrap returns a frame-aware wrapper when c is an IPv4/IPv6 UDP socket with
// an accessible RawConn. The bool reports whether the wrapper was installed.
func Wrap(c *net.UDPConn, fast bool, onClose func()) (*Conn, bool) {
	if c == nil || c.RemoteAddr() != nil {
		return nil, false
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, false
	}
	family := 0
	controlErr := raw.Control(func(fd uintptr) {
		family, err = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_DOMAIN)
	})
	if controlErr != nil || err != nil || (family != unix.AF_INET && family != unix.AF_INET6) {
		return nil, false
	}

	var direct transport.UDPConn = c
	if fast {
		direct = udpfast.Wrap(c)
	}
	wrapped := &Conn{
		UDPConn:       direct,
		udp:           c,
		raw:           raw,
		family:        family,
		onClose:       onClose,
		flushInterval: configuredFlushInterval(),
		sendmmsg:      syscallSendmmsg,
	}
	if os.Getenv("NANOKVM_WEBRTC_UDP_GSO") == "1" {
		wrapped.gsoBuffer = new([maxBatchPackets * maxPacketBytes]byte)
		if os.Getenv("NANOKVM_WEBRTC_DIAGNOSTICS") == "1" {
			wrapped.gsoStats = &gsoDiagnostics{}
		}
	}
	return wrapped, true
}

func configuredFlushInterval() time.Duration {
	if os.Getenv(batchFlushEnv) == "4000" {
		return 4 * time.Millisecond
	}
	return defaultFlushInterval
}

// BeginFrame enables bounded media buffering for this socket.
func (c *Conn) BeginFrame() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return
	}
	c.frame = true
	c.sticky = nil
}

// EndFrame flushes all buffered media and returns the first send error.
func (c *Conn) EndFrame() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return net.ErrClosed
	}
	if !c.frame {
		return nil
	}
	c.stopTimerLocked()
	err := c.flushLocked()
	c.frame = false
	if err == nil {
		err = c.sticky
	}
	c.sticky = nil
	return err
}

func (c *Conn) timerFlush(state *timerState) {
	c.mu.Lock()
	if c.timerState != state {
		c.mu.Unlock()
		return
	}
	c.timerState = nil
	c.timerArmed = false
	if !c.closed.Load() && c.frame && c.count != 0 {
		_ = c.flushLocked()
	}
	c.mu.Unlock()
}

func (c *Conn) startTimerLocked() {
	if c.timerState != nil {
		if !c.timerArmed {
			c.timerState.timer.Reset(c.flushInterval)
			c.timerArmed = true
		}
		return
	}
	state := &timerState{}
	state.timer = time.AfterFunc(c.flushInterval, func() { c.timerFlush(state) })
	c.timerState = state
	c.timerArmed = true
}

func (c *Conn) stopTimerLocked() {
	state := c.timerState
	if state == nil || !c.timerArmed {
		return
	}
	if state.timer.Stop() {
		c.timerArmed = false
		// Keep the stopped timer for the next frame. If its callback has
		// started, Stop returns false and the state is invalidated below.
		return
	}
	c.timerState = nil
	c.timerArmed = false
}

func (c *Conn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	if err := c.flushLocked(); err != nil {
		return 0, err
	}
	return c.UDPConn.Write(b)
}

func (c *Conn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if udp, ok := addr.(*net.UDPAddr); ok {
		return c.WriteToUDP(b, udp)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	if err := c.flushLocked(); err != nil {
		return 0, err
	}
	return c.UDPConn.WriteTo(b, addr)
}

func (c *Conn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	ap, valid := c.batchAddr(addr)
	n, err, handled := c.batchWriteLocked(b, ap, valid)
	if handled || err != nil {
		return n, err
	}
	return c.UDPConn.WriteToUDP(b, addr)
}

func (c *Conn) batchWriteLocked(b []byte, ap netip.AddrPort, valid bool) (int, error, bool) {
	if !c.frame || !isBatchableRTP(b) {
		if err := c.flushLocked(); err != nil {
			return 0, err, true
		}
		return 0, nil, false
	}
	if c.sticky != nil {
		return 0, c.sticky, true
	}
	if !valid || (c.count != 0 && c.addr != ap) {
		if err := c.flushLocked(); err != nil {
			return 0, err, true
		}
		return 0, nil, false
	}
	if c.count == maxBatchPackets {
		if err := c.flushLocked(); err != nil {
			return 0, err, true
		}
	}
	if len(b) > maxPacketBytes {
		if err := c.flushLocked(); err != nil {
			return 0, err, true
		}
		return 0, nil, false
	}
	if c.count == 0 {
		c.addr = ap
	}
	slot := &c.slots[c.count]
	slot.n = copy(slot.data[:], b)
	slot.addr = ap
	slot.marker = b[1]&0x80 != 0
	c.count++
	c.startTimerLocked()
	if slot.marker {
		if err := c.flushLocked(); err != nil {
			return 0, err, true
		}
	}
	return len(b), nil, true
}

func (c *Conn) WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (int, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return 0, 0, net.ErrClosed
	}
	if err := c.flushLocked(); err != nil {
		return 0, 0, err
	}
	return c.UDPConn.WriteMsgUDP(b, oob, addr)
}

func (c *Conn) SetDeadline(t time.Time) error {
	return c.UDPConn.SetDeadline(t)
}

func (c *Conn) SetWriteDeadline(t time.Time) error {
	return c.UDPConn.SetWriteDeadline(t)
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		// Close before taking mu so a blocking RawConn.Write is interrupted.
		c.closeErr = c.UDPConn.Close()
		c.mu.Lock()
		c.stopTimerLocked()
		c.frame = false
		c.count = 0
		c.sticky = net.ErrClosed
		if s := c.gsoStats; s != nil && s.attempts != 0 {
			log.Infof("WebRTC UDP GSO close: attempts=%d bundles=%d datagrams=%d fallbacks=%d", s.attempts, s.bundles, s.datagrams, s.fallbacks)
		}
		if c.gsoBuffer != nil {
			clear(c.gsoBuffer[:])
		}
		for i := range c.slots {
			c.slots[i] = packetSlot{}
		}
		c.mu.Unlock()
		if c.onClose != nil {
			c.onClose()
		}
	})
	return c.closeErr
}

func (c *Conn) ReadFromAddrPort(b []byte) (int, netip.AddrPort, error) {
	return c.udp.ReadFromUDPAddrPort(b)
}

func (c *Conn) WriteToAddrPort(b []byte, addr netip.AddrPort) (int, error) {
	if !addr.IsValid() {
		return 0, &net.AddrError{Err: "invalid address", Addr: addr.String()}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	ap, valid := c.batchAddrPort(addr)
	n, err, handled := c.batchWriteLocked(b, ap, valid)
	if handled || err != nil {
		return n, err
	}
	return c.UDPConn.WriteToUDP(b, net.UDPAddrFromAddrPort(addr))
}

func (c *Conn) batchAddr(addr *net.UDPAddr) (netip.AddrPort, bool) {
	if addr == nil || addr.Zone != "" || addr.Port < 0 || addr.Port > 0xffff {
		return netip.AddrPort{}, false
	}
	ip, ok := netip.AddrFromSlice(addr.IP)
	if !ok {
		return netip.AddrPort{}, false
	}
	return c.batchAddrPort(netip.AddrPortFrom(ip, uint16(addr.Port)))
}

func (c *Conn) batchAddrPort(addr netip.AddrPort) (netip.AddrPort, bool) {
	if !addr.IsValid() || addr.Addr().Zone() != "" {
		return netip.AddrPort{}, false
	}
	ip := addr.Addr()
	switch c.family {
	case unix.AF_INET:
		ip = ip.Unmap()
		if !ip.Is4() {
			return netip.AddrPort{}, false
		}
	case unix.AF_INET6:
		if !ip.Is6() {
			return netip.AddrPort{}, false
		}
	default:
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ip, addr.Port()), true
}

func isBatchableRTP(b []byte) bool {
	if len(b) < 2 || len(b) > maxPacketBytes {
		return false
	}
	if b[0]&0xc0 != 0x80 {
		return false
	}
	switch b[1] & 0x7f {
	case 102, 126:
		return true
	default:
		return false
	}
}

func (c *Conn) flushLocked() error {
	if c.count == 0 {
		return c.sticky
	}
	c.stopTimerLocked()

	count := c.count
	sent, err, handled := c.sendGSOLocked(count)
	if !handled {
		var messages [maxBatchPackets]mmsghdr
		var iovs [maxBatchPackets]unix.Iovec
		var addr4 [maxBatchPackets]unix.RawSockaddrInet4
		var addr6 [maxBatchPackets]unix.RawSockaddrInet6
		for i := 0; i < c.count; i++ {
			slot := &c.slots[i]
			iovs[i] = unix.Iovec{
				Base: &slot.data[0],
				Len:  uint64(slot.n),
			}
			messages[i].msg.Iov = &iovs[i]
			messages[i].msg.Iovlen = 1
			if c.family == unix.AF_INET {
				ip := slot.addr.Addr().As4()
				addr4[i].Family = unix.AF_INET
				binary.BigEndian.PutUint16((*[2]byte)(unsafe.Pointer(&addr4[i].Port))[:], slot.addr.Port())
				copy(addr4[i].Addr[:], ip[:])
				messages[i].msg.Name = (*byte)(unsafe.Pointer(&addr4[i]))
				messages[i].msg.Namelen = uint32(unsafe.Sizeof(addr4[i]))
			} else {
				ip := slot.addr.Addr().As16()
				addr6[i].Family = unix.AF_INET6
				binary.BigEndian.PutUint16((*[2]byte)(unsafe.Pointer(&addr6[i].Port))[:], slot.addr.Port())
				copy(addr6[i].Addr[:], ip[:])
				messages[i].msg.Name = (*byte)(unsafe.Pointer(&addr6[i]))
				messages[i].msg.Namelen = uint32(unsafe.Sizeof(addr6[i]))
			}
		}

		sent, err = c.sendMessagesLocked(&messages, count)
	}
	if errors.Is(err, syscall.ENOSYS) && sent == 0 {
		_, fallbackErr := c.fallbackLocked(0, count)
		if fallbackErr == nil {
			err = nil
		} else {
			err = fallbackErr
		}
	} else if err != nil && sent > 0 && sent < count &&
		!errors.Is(err, os.ErrDeadlineExceeded) && !errors.Is(err, net.ErrClosed) {
		_, fallbackErr := c.fallbackLocked(sent, count)
		if fallbackErr != nil {
			err = fallbackErr
		}
	}
	for i := 0; i < count; i++ {
		c.slots[i] = packetSlot{}
	}
	c.count = 0
	if err != nil && c.sticky == nil {
		c.sticky = err
	}
	return err
}

func (c *Conn) fallbackLocked(start, end int) (int, error) {
	for i := start; i < end; i++ {
		slot := &c.slots[i]
		n, err := c.UDPConn.WriteToUDP(slot.data[:slot.n], net.UDPAddrFromAddrPort(slot.addr))
		if err != nil {
			return i, err
		}
		if n != slot.n {
			return i, io.ErrShortWrite
		}
	}
	return end, nil
}

func (c *Conn) sendMessagesLocked(messages *[maxBatchPackets]mmsghdr, count int) (int, error) {
	sent := 0
	var callbackErr error
	rawErr := c.raw.Write(func(fd uintptr) bool {
		for sent < count {
			remaining := uintptr(count - sent)
			n, errno := c.sendmmsg(fd, unsafe.Pointer(&messages[sent]), remaining)
			// RawSyscall6 returns uintptr(-1) alongside errno. Treat an
			// errored overlarge result as zero, then preserve any valid
			// prefix reported with an error before handling that error.
			if n > remaining {
				if errno == 0 {
					callbackErr = io.ErrShortWrite
					return true
				}
				n = 0
			}
			if errno == syscall.EINTR {
				continue
			}
			sent += int(n)
			if errno == syscall.EAGAIN || errno == syscall.EWOULDBLOCK {
				return false
			}
			if errno != 0 {
				callbackErr = os.NewSyscallError("sendmmsg", errno)
				return true
			}
			if n == 0 {
				callbackErr = io.ErrUnexpectedEOF
				return true
			}
		}
		return true
	})
	runtime.KeepAlive(messages)
	runtime.KeepAlive(c)
	if rawErr != nil {
		return sent, rawErr
	}
	if callbackErr != nil {
		return sent, callbackErr
	}
	if sent != count {
		return sent, io.ErrUnexpectedEOF
	}
	return sent, nil
}

func syscallSendmmsg(fd uintptr, messages unsafe.Pointer, count uintptr) (uintptr, syscall.Errno) {
	r1, _, errno := unix.RawSyscall6(unix.SYS_SENDMMSG, fd, uintptr(messages), count, 0, 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return r1, 0
}

var _ transport.UDPConn = (*Conn)(nil)
