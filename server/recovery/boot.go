package recovery

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"NanoKVM-Server/internal/apkrun"
	"NanoKVM-Server/osupdate"
)

func Live() Engine {
	board, _ := os.ReadFile("/etc/kvm/hw")
	return Engine{Root: "/", Version: osupdate.InstalledVersion(), Board: strings.TrimSpace(string(board))}
}

// Available requires both halves of the upgrade: nanokvm + nanokvm-base.
func Available() bool {
	b, err := os.ReadFile("/etc/init.d/nanokvm-board")
	return err == nil && strings.Contains(string(b), "nkos-update recovery-resume")
}
func Boot() error {
	e := Live()
	if !e.Pending() {
		return nil
	}
	// The pending marker deliberately blocks normal osupdate.Lock callers.
	processes, _ := filepath.Glob("/proc/[0-9]*/comm")
	for _, name := range processes {
		b, _ := os.ReadFile(name)
		if strings.TrimSpace(string(b)) == "NanoKVM-Server" {
			return errors.New("recovery-resume is only for early boot; reboot the device")
		}
	}
	lock, err := osupdate.RecoveryLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	return e.Resume(func(names []string) error {
		if len(names) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			args := append([]string{"del", "--no-network", "--no-progress"}, names...)
			if output, err := apkrun.Command(ctx, "/sbin/apk", args...).CombinedOutput(); err != nil {
				return fmt.Errorf("APK reset failed: %w (%s)", err, strings.TrimSpace(string(output)))
			}
		}
		if err := write("/", "usr/bin/picoclaw", nil); err != nil {
			return err
		}
		return removeACMEBinaries("/")
	})
}

// Delete only the known acme.sh program tree, never its parent or config data.
// WalkDir does not follow symlinks; aliases are rejected before any deletion.
func removeACMEBinaries(root string) error {
	type entry struct {
		name string
		dir  bool
	}
	paths := []entry{}
	var collect func(string) error
	collect = func(name string) error {
		fd, base, err := parent(root, name, false)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		n, err := unix.Openat(fd, base, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		dir := os.NewFile(uintptr(n), name)
		entries, err := dir.ReadDir(4097)
		dir.Close()
		if err != nil && err != io.EOF {
			return err
		}
		paths = append(paths, entry{name, true})
		for _, item := range entries {
			if len(paths) > 4096 || strings.Count(name, "/") > 12 {
				return errors.New("excessive acme.sh program tree")
			}
			child := name + "/" + item.Name()
			if item.IsDir() {
				if err = collect(child); err != nil {
					return err
				}
			} else {
				if !item.Type().IsRegular() {
					return errors.New("unsafe acme.sh program entry")
				}
				paths = append(paths, entry{child, false})
			}
		}
		return nil
	}
	if err := collect("etc/kvm/acme/bin"); err != nil {
		return err
	}
	for i := len(paths) - 1; i >= 0; i-- {
		item := paths[i]
		if !item.dir {
			if err := write(root, item.name, nil); err != nil {
				return err
			}
			continue
		}
		fd, base, err := parent(root, item.name, false)
		if err != nil {
			return err
		}
		err = unix.Unlinkat(fd, base, unix.AT_REMOVEDIR)
		if err == nil {
			err = unix.Fsync(fd)
		}
		unix.Close(fd)
		if err != nil {
			return err
		}
	}
	return nil
}

// Reboot is detached from the web process; the durable journal already exists.
func Reboot() error {
	cmd := exec.Command(osupdate.Helper, "recovery-reboot")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
