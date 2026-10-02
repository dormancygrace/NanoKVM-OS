package osupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Base holds the shared update lock. APK, software, VPN, attended-image and
// add-on operations all serialise on Base/lock, so the path must stay stable.
const Base = "/kvmapp/.os-update"

// Helper runs APK and software transactions with the inherited update lock,
// detached from the web server (see cmd/nkos-update).
const Helper = "/usr/sbin/nkos-update"

const versionFile = "/kvmapp/version"

func writeJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeAtomic(name, b, 0600)
}

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

func syncDir(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func readJSON(name string, v any) error {
	b, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// InstalledVersion reports the application release recorded by nanokvm-app.
func InstalledVersion() string {
	v, _ := os.ReadFile(versionFile)
	return strings.TrimSpace(string(v))
}

// HashFile returns the hex SHA-256 digest of a file.
func HashFile(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Lock takes the exclusive, non-blocking update lock shared by every package
// and image operation. The caller releases it by closing the returned file.
func Lock() (*os.File, error) {
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
	return f, nil
}
