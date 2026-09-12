package remotemedia

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const DevicePath = "/dev/nbd0"
const (
	nbdSetSock       = 0xab00
	nbdSetBlockSize  = 0xab01
	nbdDoIt          = 0xab03
	nbdClearSock     = 0xab04
	nbdSetSizeBlocks = 0xab07
	nbdDisconnect    = 0xab08
	nbdSetTimeout    = 0xab09
	nbdSetFlags      = 0xab0a
)

// Device owns one kernel attachment. Close disconnects outstanding block reads
// before waiting for the kernel ioctl; it never unloads another user's module.
type Device struct {
	file         *os.File
	kernelSocket *os.File
	conn         net.Conn
	done         chan error
	stopped      chan struct{}
	once         sync.Once
}

func ioctl(file *os.File, cmd uintptr, value uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), cmd, value)
	if errno != 0 {
		return errno
	}
	return nil
}

func Open(size uint64, read ReadBlock) (*Device, error) {
	if err := ValidateImage(size); err != nil {
		return nil, err
	}
	if _, err := os.Stat(DevicePath); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "modprobe", "nbd", "nbds_max=1", "max_part=0")
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("NBD support unavailable: %w", err)
		}
	}
	file, err := os.OpenFile(DevicePath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("remote media device is busy")
	}
	if pid, err := os.ReadFile("/sys/block/nbd0/pid"); err == nil && strings.TrimSpace(string(pid)) != "" {
		file.Close()
		return nil, errors.New("NBD device is already connected")
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		file.Close()
		return nil, err
	}
	kernelSocket := os.NewFile(uintptr(fds[0]), "nbd-kernel")
	userSocket := os.NewFile(uintptr(fds[1]), "nbd-browser")
	conn, err := net.FileConn(userSocket)
	userSocket.Close()
	if err != nil {
		kernelSocket.Close()
		file.Close()
		return nil, err
	}
	d := &Device{file: file, kernelSocket: kernelSocket, conn: conn, done: make(chan error, 1), stopped: make(chan struct{})}
	for _, op := range []struct{ cmd, value uintptr }{
		{nbdSetBlockSize, 512}, {nbdSetSizeBlocks, uintptr(size / 512)},
		{nbdSetTimeout, 20}, {nbdSetFlags, 3}, // HAS_FLAGS | READ_ONLY
		{nbdSetSock, kernelSocket.Fd()},
	} {
		if err := ioctl(file, op.cmd, op.value); err != nil {
			conn.Close()
			kernelSocket.Close()
			file.Close()
			return nil, fmt.Errorf("configure NBD: %w", err)
		}
	}
	go func() {
		err := Serve(conn, size, read)
		conn.Close()
		_ = err // Kernel sees socket closure as failed I/O; owner handles disconnect.
	}()
	go func() { d.done <- ioctl(file, nbdDoIt, 0); close(d.stopped) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-d.done:
			d.done <- err
			d.Close()
			return nil, fmt.Errorf("NBD stopped before attachment: %v", err)
		default:
		}
		if pid, err := os.ReadFile("/sys/block/nbd0/pid"); err == nil && strings.TrimSpace(string(pid)) != "" {
			if err := os.WriteFile("/sys/block/nbd0/queue/max_sectors_kb", []byte("1024"), 0600); err != nil {
				d.Close()
				return nil, fmt.Errorf("bound NBD request size: %w", err)
			}
			return d, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.Close()
	return nil, errors.New("NBD attachment timed out")
}

func (d *Device) Close() {
	d.once.Do(func() {
		d.conn.Close()
		_ = ioctl(d.file, nbdDisconnect, 0)
		<-d.done
		_ = ioctl(d.file, nbdClearSock, 0)
		d.kernelSocket.Close()
		d.file.Close()
	})
}

func (d *Device) Stopped() <-chan struct{} { return d.stopped }
