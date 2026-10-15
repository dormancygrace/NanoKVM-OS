package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// The private mount is never exported by the image browser or USB gadget.
// All callers hold userDiskMu until unmount completes (including downloads).
var userDiskMountpoint = "/run/nanokvm/user-disk"
var mountUserDisk = mountUserDiskImage

func userDiskMounted() (bool, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	escaped := strings.NewReplacer("\\", "\\134", " ", "\\040", "\t", "\\011", "\n", "\\012").Replace(userDiskMountpoint)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[4] == escaped {
			return true, nil
		}
	}
	return false, nil
}

// Fail closed after a failed unmount or a server crash. A later file request
// can retire our stale mount, but USB attachment/reformat must never ignore it.
func userDiskUnmounted() error {
	mounted, err := userDiskMounted()
	if err != nil {
		return err
	}
	if mounted {
		return ErrUserDiskBusy
	}
	return nil
}

func mountUserDiskImage(writable bool) (*os.Root, func() error, error) {
	// Previous requests normally unmount. A process killed during a transfer
	// leaves a private mount; clean that up before opening a new operation.
	mounted, err := userDiskMounted()
	if err != nil {
		return nil, nil, err
	}
	if mounted {
		if err = unix.Unmount(userDiskMountpoint, 0); err != nil {
			return nil, nil, err
		}
	}
	if err = os.MkdirAll(userDiskMountpoint, 0700); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(userDiskMountpoint)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("invalid user disk mount point")
	}
	flags, loopFlags := os.O_RDONLY, uint32(unix.LO_FLAGS_AUTOCLEAR|unix.LO_FLAGS_READ_ONLY)
	mountFlags := uintptr(unix.MS_NODEV | unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_RDONLY)
	if writable {
		flags, loopFlags = os.O_RDWR, unix.LO_FLAGS_AUTOCLEAR
		mountFlags &^= unix.MS_RDONLY
	}
	image, err := os.OpenFile(userDiskImage, flags|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	defer image.Close()
	imageInfo, err := image.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !imageInfo.Mode().IsRegular() {
		return nil, nil, errors.New("user disk is not a regular image")
	}
	control, err := os.OpenFile("/dev/loop-control", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	defer control.Close()
	var loop *os.File
	// LOOP_CTL_GET_FREE is advisory: another process may claim the device.
	for attempt := 0; attempt < 8; attempt++ {
		number, e := unix.IoctlRetInt(int(control.Fd()), unix.LOOP_CTL_GET_FREE)
		if e != nil {
			return nil, nil, e
		}
		loop, e = os.OpenFile(fmt.Sprintf("/dev/loop%d", number), flags, 0)
		if e != nil {
			return nil, nil, e
		}
		e = unix.IoctlLoopConfigure(int(loop.Fd()), &unix.LoopConfig{
			Fd: uint32(image.Fd()), Info: unix.LoopInfo64{Flags: loopFlags},
		})
		if e == nil {
			break
		}
		loop.Close()
		loop = nil
		if !errors.Is(e, unix.EBUSY) {
			return nil, nil, e
		}
	}
	if loop == nil {
		return nil, nil, errors.New("no free loop device")
	}
	// AUTOCLEAR releases the loop on failure, or after the mount is gone.
	defer loop.Close()
	if err = unix.Mount(loop.Name(), userDiskMountpoint, "vfat", mountFlags, "utf8=1,umask=0077"); err != nil {
		return nil, nil, fmt.Errorf("mount user disk: %w", err)
	}
	root, err := os.OpenRoot(userDiskMountpoint)
	if err != nil {
		return nil, nil, errors.Join(err, unix.Unmount(userDiskMountpoint, 0))
	}
	cleanup := func() error {
		closeErr := root.Close()
		// Never lazy-unmount: attachment must wait for a real flush/unmount.
		return errors.Join(closeErr, unix.Unmount(userDiskMountpoint, 0))
	}
	return root, cleanup, nil
}

func userDiskAvailable() error {
	// A read failure is not permission to mount the image while the PC owns it.
	for _, marker := range []string{userDiskFlag, userDiskLun} {
		data, err := os.ReadFile(marker)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if strings.TrimSpace(string(data)) == filepath.Clean(userDiskImage) {
			return errUserDiskHost
		}
	}
	return nil
}
