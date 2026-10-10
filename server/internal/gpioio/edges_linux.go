//go:build linux

package gpioio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ErrEdgesUnsupported marks a line that cannot deliver edge events, such as a
// sysfs line or a controller without interrupts. Callers should poll instead.
var ErrEdgesUnsupported = errors.New("GPIO edge events unsupported")

// EdgeEvents summarises the edge events drained by one read.
type EdgeEvents struct {
	Count int
	Last  time.Time // when the newest event happened, on time.Now's clock
}

// EdgeLine is an input line that also reports its level changes.
type EdgeLine interface {
	Line
	// WaitEdges parks in the Go netpoller until at least one edge event is
	// queued, then drains the queued events in a single read. Close unblocks it.
	WaitEdges() (EdgeEvents, error)
}

const (
	edgeEventSize = int(unsafe.Sizeof(unix.GPIOV2LineEvent{}))
	// The kernel queues 16 events per line by default; one read takes them all.
	edgeReadEvents = 64
	// ENOTSUPP is the kernel-internal errno some GPIO drivers leak to userspace.
	errnoENOTSUPP = syscall.Errno(524)
)

// OpenEdges requests a GPIO-v2 line as an input with both-edge detection.
// The error wraps ErrEdgesUnsupported when the line can only be polled.
func OpenEdges(device string) (EdgeLine, error) {
	if !strings.HasPrefix(device, "gpio-v2:") {
		return nil, fmt.Errorf("%w: %q is not a GPIO-v2 line", ErrEdgesUnsupported, device)
	}
	fd, err := requestV2(device, edgeRequest)
	if err != nil {
		return nil, unsupportedEdges(err)
	}
	line, err := newEdgeLine(fd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", device, err)
	}
	return line, nil
}

func edgeRequest(offset uint32) unix.GPIOV2LineRequest {
	req := request(offset, false)
	req.Config.Flags |= flagEdgeRising | flagEdgeFalling
	return req
}

// unsupportedEdges marks the errnos a chip without edge support returns.
func unsupportedEdges(err error) error {
	for _, errno := range []syscall.Errno{unix.EINVAL, unix.ENXIO, unix.EOPNOTSUPP, errnoENOTSUPP} {
		if errors.Is(err, errno) {
			return fmt.Errorf("%w: %w", ErrEdgesUnsupported, err)
		}
	}
	return err
}

type edgeLine struct {
	file *os.File
	conn syscall.RawConn
	buf  []byte

	// Event timestamps use CLOCK_MONOTONIC, the clock behind time.Now's
	// monotonic reading; these two readings of it map one onto the other.
	monoBase int64
	base     time.Time
}

// newEdgeLine takes ownership of fd and registers it with the netpoller.
func newEdgeLine(fd int) (*edgeLine, error) {
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "gpio-line")
	// Only a file registered with the netpoller supports deadlines; any other
	// read would block a thread or fail with EAGAIN.
	if err := file.SetReadDeadline(time.Time{}); err != nil {
		file.Close()
		return nil, fmt.Errorf("%w: line fd is not pollable: %w", ErrEdgesUnsupported, err)
	}
	conn, err := file.SyscallConn()
	if err != nil {
		file.Close()
		return nil, err
	}
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &now); err != nil {
		file.Close()
		return nil, err
	}
	return &edgeLine{
		file:     file,
		conn:     conn,
		buf:      make([]byte, edgeReadEvents*edgeEventSize),
		monoBase: now.Nano(),
		base:     time.Now(),
	}, nil
}

func (l *edgeLine) Close() error { return l.file.Close() }

func (l *edgeLine) Set(bool) error { return errors.New("GPIO edge line is an input") }

// Get uses Control rather than Fd, which would put the file back in blocking mode.
func (l *edgeLine) Get() (bool, error) {
	values := unix.GPIOV2LineValues{Mask: 1}
	var ioErr error
	err := l.conn.Control(func(fd uintptr) {
		ioErr = ioctl(int(fd), unix.GPIO_V2_LINE_GET_VALUES_IOCTL, unsafe.Pointer(&values))
	})
	if err == nil {
		err = ioErr
	}
	return values.Bits&1 != 0, err
}

func (l *edgeLine) WaitEdges() (EdgeEvents, error) {
	n, err := l.file.Read(l.buf)
	if err != nil {
		return EdgeEvents{}, unsupportedEdges(err)
	}
	return l.parse(l.buf[:n])
}

func (l *edgeLine) parse(data []byte) (EdgeEvents, error) {
	if len(data) == 0 || len(data)%edgeEventSize != 0 {
		return EdgeEvents{}, fmt.Errorf("GPIO edge read returned %d bytes", len(data))
	}
	var newest uint64
	for record := data; len(record) > 0; record = record[edgeEventSize:] {
		newest = max(newest, binary.NativeEndian.Uint64(record))
	}
	last := l.base.Add(time.Duration(int64(newest) - l.monoBase))
	if now := time.Now(); last.After(now) {
		last = now
	}
	return EdgeEvents{Count: len(data) / edgeEventSize, Last: last}, nil
}
