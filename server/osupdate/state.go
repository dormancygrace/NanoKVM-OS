package osupdate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"NanoKVM-Server/standby"
)

// Base holds the shared update lock. APK, software, VPN and add-on
// operations all serialise on Base/lock, so the path must stay stable.
const Base = "/kvmapp/.os-update"

// Helper runs APK and software transactions with the inherited update lock,
// detached from the web server (see cmd/nkos-update).
const Helper = "/usr/sbin/nkos-update"

const versionFile = "/kvmapp/version"

func writeAtomic(name string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".write-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if err = os.Rename(tmp, name); err != nil {
		return err
	}
	return syncDir(filepath.Dir(name))
}

// writeRepositories replaces the repository list. A list that is a symbolic
// link is written through it: the link stays and its target is replaced. The
// new file takes the old one's mode and owner (a list kept private stays so);
// a list that does not exist yet is created with mode 0644.
func writeRepositories(b []byte) error {
	name := repositoriesPath
	if target, err := filepath.EvalSymlinks(name); err == nil {
		name = target
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, err := os.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return writeAtomic(name, b, 0644)
	} else if err != nil {
		return err
	}
	return writeAtomicOwned(name, b, info)
}

// writeAtomicOwned is writeAtomic with the mode and owner of like.
func writeAtomicOwned(name string, b []byte, like os.FileInfo) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".write-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	err = f.Chmod(like.Mode().Perm())
	if st, ok := like.Sys().(*syscall.Stat_t); ok && err == nil {
		err = f.Chown(int(st.Uid), int(st.Gid))
	}
	if err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if err = os.Rename(tmp, name); err != nil {
		return err
	}
	return syncDir(filepath.Dir(name))
}

func syncDir(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// InstalledVersion reports the application release recorded by nanokvm.
func InstalledVersion() string {
	v, _ := os.ReadFile(versionFile)
	return strings.TrimSpace(string(v))
}

// Lock takes the exclusive, non-blocking update lock shared by every package
// and image operation. The caller releases it by closing the returned file.
func Lock() (*os.File, error) {
	f, err := RecoveryLock()
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat("/var/lib/nanokvm-recovery/pending.json"); !errors.Is(err, os.ErrNotExist) {
		f.Close()
		return nil, errors.New("settings recovery pending; reboot before changing packages")
	}
	return f, nil
}

// RecoveryLock shares the normal update lock but permits the early-boot worker
// to finish an already queued operation. No normal package caller uses it.
func RecoveryLock() (*os.File, error) {
	if err := os.MkdirAll(Base, 0700); err != nil {
		return nil, err
	}
	baseInfo, err := os.Lstat(Base)
	if err != nil || !baseInfo.IsDir() || baseInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe update state directory")
	}
	fd, err := syscall.Open(Base+"/lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "update-lock")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("unsafe update lock")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another update operation is in progress")
	}
	if err = standby.Pending(); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
