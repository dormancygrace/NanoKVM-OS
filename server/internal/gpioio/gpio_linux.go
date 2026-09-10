//go:build linux

// Package gpioio provides GPIO-v2 requests and the existing sysfs image ABI.
package gpioio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Direction bits from enum gpio_v2_line_flag in linux/gpio.h.
const (
	flagInput  uint64 = 1 << 2
	flagOutput uint64 = 1 << 3
)

type Line interface {
	Set(bool) error
	Get() (bool, error)
	Close() error
}

// Controller rejects overlapping ATX pulses rather than queuing a later press.
type Controller struct{ mu sync.Mutex }

var ErrBusy = errors.New("another ATX pulse is in progress")

func (c *Controller) Pulse(ctx context.Context, duration time.Duration, request func() (Line, error)) (err error) {
	if duration <= 0 || duration > time.Minute {
		return errors.New("ATX duration must be between 1 ns and 60 seconds")
	}
	if !c.mu.TryLock() {
		return ErrBusy
	}
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := request()
	if err != nil {
		return err
	}
	// Release the switch even if activation fails or the HTTP request is cancelled.
	defer func() { err = errors.Join(err, line.Set(false), line.Close()) }()
	if err = line.Set(true); err != nil {
		return err
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func Path(label string, offset uint32) string { return fmt.Sprintf("gpio-v2:%s:%d", label, offset) }

func ioctl(fd int, command uint, p unsafe.Pointer) error {
	_, _, err := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(command), uintptr(p))
	runtime.KeepAlive(p)
	if err != 0 {
		return err
	}
	return nil
}

// ChipInfo reads metadata only; it never requests or changes a GPIO line.
func ChipInfo(path string) (unix.GPIOChipInfo, error) {
	var info unix.GPIOChipInfo
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return info, err
	}
	defer unix.Close(fd)
	err = ioctl(fd, unix.GPIO_GET_CHIPINFO_IOCTL, unsafe.Pointer(&info))
	return info, err
}

// LineInfo reads ABI-v2 metadata only, including whether a line is in use.
func LineInfo(path string, offset uint32) (unix.GPIOV2LineInfo, error) {
	info := unix.GPIOV2LineInfo{Offset: offset}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return info, err
	}
	defer unix.Close(fd)
	err = ioctl(fd, unix.GPIO_V2_GET_LINEINFO_IOCTL, unsafe.Pointer(&info))
	return info, err
}

func Open(device string, output bool) (Line, error) {
	if !strings.HasPrefix(device, "gpio-v2:") {
		if !strings.HasPrefix(device, "/sys/class/gpio/gpio") || !strings.HasSuffix(device, "/value") {
			return nil, fmt.Errorf("invalid GPIO device %q", device)
		}
		if _, err := os.Stat(device); err != nil {
			return nil, err
		}
		return &sysfsLine{path: device}, nil
	}
	parts := strings.Split(device, ":")
	if len(parts) != 3 || parts[1] == "" {
		return nil, fmt.Errorf("invalid GPIO-v2 device %q", device)
	}
	offset, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob("/dev/gpiochip*")
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		var info unix.GPIOChipInfo
		err = ioctl(fd, unix.GPIO_GET_CHIPINFO_IOCTL, unsafe.Pointer(&info))
		if err != nil {
			unix.Close(fd)
			return nil, err
		}
		if unix.ByteSliceToString(info.Label[:]) != parts[1] {
			unix.Close(fd)
			continue
		}
		if uint32(offset) >= info.Lines {
			unix.Close(fd)
			return nil, errors.New("GPIO line outside controller")
		}
		req := request(uint32(offset), output)
		err = ioctl(fd, unix.GPIO_V2_GET_LINE_IOCTL, unsafe.Pointer(&req))
		unix.Close(fd)
		if err != nil {
			return nil, fmt.Errorf("request %s: %w", device, err)
		}
		return &cdevLine{fd: int(req.Fd)}, nil
	}
	return nil, fmt.Errorf("GPIO controller %q not found", parts[1])
}

func request(offset uint32, output bool) unix.GPIOV2LineRequest {
	req := unix.GPIOV2LineRequest{Num_lines: 1}
	req.Offsets[0] = offset
	copy(req.Consumer[:], "nanokvm-atx")
	req.Config.Flags = flagInput
	if output {
		req.Config.Flags = flagOutput
		req.Config.Num_attrs = 1
		req.Config.Attrs[0].Mask = 1
		req.Config.Attrs[0].Attr.Id = unix.GPIO_V2_LINE_ATTR_ID_OUTPUT_VALUES
		// Attr.Flags is the generated union's uint64 view of output values.
		req.Config.Attrs[0].Attr.Flags = 0 // Request atomically with switch released.
	}
	return req
}

type cdevLine struct{ fd int }

func (l *cdevLine) Close() error { return unix.Close(l.fd) }
func (l *cdevLine) Set(active bool) error {
	values := unix.GPIOV2LineValues{Mask: 1}
	if active {
		values.Bits = 1
	}
	return ioctl(l.fd, unix.GPIO_V2_LINE_SET_VALUES_IOCTL, unsafe.Pointer(&values))
}
func (l *cdevLine) Get() (bool, error) {
	values := unix.GPIOV2LineValues{Mask: 1}
	err := ioctl(l.fd, unix.GPIO_V2_LINE_GET_VALUES_IOCTL, unsafe.Pointer(&values))
	return values.Bits&1 != 0, err
}

type sysfsLine struct{ path string }

func (l *sysfsLine) Close() error { return nil }
func (l *sysfsLine) Set(active bool) error {
	value := "0"
	if active {
		value = "1"
	}
	return os.WriteFile(l.path, []byte(value), 0o666)
}
func (l *sysfsLine) Get() (bool, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(string(data)) {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("invalid GPIO value %q", data)
	}
}
