package standby

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Shutdown is init's shutdown action. Keep a descriptor opened before the
// services stop for each persistent mount: syncfs then observes writeback
// errors over the entire shutdown interval, even if mount-ro remounted it.
// These descriptors also keep OpenRC from unmounting storage before that check.
// They close before PID1's final non-lazy unmount.
func Shutdown() error {
	if os.Getppid() != 1 {
		return errors.New("shutdown action must be started by init")
	}
	mounts, err := ReadMounts()
	var files []*os.File
	var results []error
	if err != nil {
		results = append(results, err)
	}
	for _, m := range mounts {
		if !blockFS(m.Type) {
			continue
		}
		f, e := os.Open(m.Point)
		if e != nil {
			results = append(results, e)
		} else {
			files = append(files, f)
		}
	}
	// OpenRC clears /run/openrc/failed before it returns, and its overall
	// exit status does not propagate individual stop failures. Watch events,
	// not just the final directory listing.
	watch, watchErr := watchFailures("/run/openrc/failed")
	if watchErr != nil {
		results = append(results, watchErr)
	}
	cmd := exec.Command("/sbin/openrc", "shutdown")
	// OpenRC's umount retry uses fuser -k; it would kill this supervisor for
	// holding the writeback-error descriptors. Keep mounts for the new PID1,
	// which owns the checked, non-lazy release after all service stops.
	cmd.Env = append(os.Environ(), "RC_NO_UMOUNTS=/:/.+")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if e := cmd.Run(); e != nil {
		results = append(results, fmt.Errorf("OpenRC shutdown: %w", e))
	}
	for _, f := range files {
		if e := unix.Syncfs(int(f.Fd())); e != nil {
			results = append(results, fmt.Errorf("shutdown writeback %s: %w", f.Name(), e))
		}
		if e := f.Close(); e != nil {
			results = append(results, e)
		}
	}
	if watch != nil {
		if e := watch.result(); e != nil {
			results = append(results, e)
		}
		unix.Close(watch.fd)
	}

	err = errors.Join(results...)
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	if e := os.WriteFile(Directory+"/shutdown-result", []byte(result), 0600); e != nil {
		return e
	}
	return err
}
func shutdownResult() error {
	b, e := os.ReadFile(Directory + "/shutdown-result")
	if e != nil {
		return fmt.Errorf("shutdown completion was not recorded: %w", e)
	}
	if string(b) != "ok" {
		return fmt.Errorf("shutdown did not complete cleanly: %s", b)
	}
	return nil
}

// A clean OpenRC stop and leaveStorage's noUserProcesses check precede this.
// dw_wdt may keep HW_RUNNING set after magic close; the kernel then feeds it.
// The optional "state" attribute reports WDOG_ACTIVE (userspace ownership),
// not HW_RUNNING. Missing CONFIG_WATCHDOG_SYSFS is not a failed shutdown.
func watchdogsReleased(sysfs string) error {
	entries, e := os.ReadDir(sysfs)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, entry := range entries {
		b, e := os.ReadFile(filepath.Join(sysfs, entry.Name(), "state"))
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if string(b) != "inactive\n" {
			return fmt.Errorf("watchdog %s has not released userspace ownership", entry.Name())
		}
	}
	return nil
}

type failureWatch struct {
	fd       int
	existing error
}

func watchFailures(directory string) (*failureWatch, error) {
	if e := os.MkdirAll(directory, 0755); e != nil {
		return nil, e
	}
	fd, e := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if e != nil {
		return nil, e
	}
	if _, e = unix.InotifyAddWatch(fd, directory, unix.IN_ONLYDIR|unix.IN_CREATE|unix.IN_MOVED_TO|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF); e != nil {
		unix.Close(fd)
		return nil, e
	}
	w := &failureWatch{fd: fd}
	entries, e := os.ReadDir(directory)
	if e != nil {
		unix.Close(fd)
		return nil, e
	}
	if len(entries) > 0 {
		w.existing = fmt.Errorf("OpenRC has a failed service: %s", entries[0].Name())
	}
	return w, nil
}
func (w *failureWatch) result() error {
	if w.existing != nil {
		return w.existing
	}
	buf := make([]byte, 16384)
	for {
		n, e := unix.Read(w.fd, buf)
		if e == unix.EAGAIN {
			return nil
		}
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return e
		}
		if n == 0 {
			return errors.New("OpenRC failure watch ended unexpectedly")
		}
		for offset := 0; offset < n; {
			if offset+16 > n {
				return errors.New("truncated OpenRC failure event")
			}
			mask := binary.NativeEndian.Uint32(buf[offset+4 : offset+8])
			length := int(binary.NativeEndian.Uint32(buf[offset+12 : offset+16]))
			if offset+16+length > n {
				return errors.New("truncated OpenRC failure name")
			}
			if mask&(unix.IN_Q_OVERFLOW|unix.IN_IGNORED|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF) != 0 {
				return errors.New("OpenRC failure monitoring was interrupted")
			}
			if mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
				name := strings.TrimRight(string(buf[offset+16:offset+16+length]), "\x00")
				return fmt.Errorf("OpenRC service failed or refused shutdown: %s", name)
			}
			offset += 16 + length
		}
	}
}
