package recovery

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// Every component is opened without following symlinks. os.Root alone would
// allow an in-root symlink to alias another setting or a protected system file.
func parent(root, name string, create bool) (int, string, error) {
	if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") {
		return -1, "", errors.New("unsafe relative path")
	}
	fd, err := unix.Open(root, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, "", err
	}
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(e, unix.ENOENT) && create {
			e = unix.Mkdirat(fd, part, 0700)
			if e == nil {
				e = unix.Fsync(fd)
			}
			if e == nil || errors.Is(e, unix.EEXIST) {
				next, e = unix.Openat(fd, part, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			}
		}
		unix.Close(fd)
		if e != nil {
			return -1, "", e
		}
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}
func read(root, name string, limit int) (*File, error) {
	fd, base, err := parent(root, name, false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer unix.Close(fd)
	n, err := unix.Openat(fd, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ELOOP) && name == "etc/localtime" {
		target := make([]byte, 4096)
		size, e := unix.Readlinkat(fd, base, target)
		if e != nil {
			return nil, e
		}
		dest := string(target[:size])
		if !strings.HasPrefix(dest, "/") {
			dest = path.Join("/etc", dest)
		}
		if !strings.HasPrefix(path.Clean(dest), "/usr/share/zoneinfo/") {
			return nil, errors.New("unsafe time zone link")
		}
		r, e := os.OpenRoot(root)
		if e != nil {
			return nil, e
		}
		defer r.Close()
		f, e := r.Open(strings.TrimPrefix(path.Clean(dest), "/"))
		if e != nil {
			return nil, e
		}
		defer f.Close()
		data, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
		if e != nil || len(data) > limit {
			return nil, errors.New("invalid time zone data")
		}
		return &File{Path: name, Data: data, Mode: 0644}, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(n), base)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > int64(limit) {
		return nil, fmt.Errorf("not a bounded regular file: %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("file grew beyond limit")
	}
	return &File{Path: name, Data: data, Mode: uint32(st.Mode().Perm())}, nil
}
func write(root, name string, value *File) error {
	fd, base, err := parent(root, name, value != nil)
	if err != nil {
		if value == nil && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer unix.Close(fd)
	// Refuse aliases, directories and devices even for a delete.
	var st unix.Stat_t
	err = unix.Fstatat(fd, base, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && st.Mode&unix.S_IFMT != unix.S_IFREG && !(name == "etc/localtime" && st.Mode&unix.S_IFMT == unix.S_IFLNK) {
		return fmt.Errorf("unsafe destination: %s", name)
	}
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	if value == nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		if err = unix.Unlinkat(fd, base, 0); err != nil {
			return err
		}
		return unix.Fsync(fd)
	}
	// A deterministic private scratch name bounds leftovers after power loss.
	sum := sha256.Sum256([]byte(base))
	tmp := fmt.Sprintf(".nkos-recovery-%x", sum[:8])
	var scratch unix.Stat_t
	if err = unix.Fstatat(fd, tmp, &scratch, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if scratch.Mode&unix.S_IFMT != unix.S_IFREG {
			return errors.New("unsafe recovery scratch file")
		}
		if err = unix.Unlinkat(fd, tmp, 0); err != nil {
			return err
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return err
	}
	n, err := unix.Openat(fd, tmp, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(fd, tmp, 0)
	f := os.NewFile(uintptr(n), tmp)
	_, err = f.Write(value.Data)
	if err == nil {
		var filesystem unix.Statfs_t
		err = unix.Fstatfs(n, &filesystem)
		// VFAT stores no Unix modes. Its mount permissions apply to /boot.
		if err == nil && filesystem.Type != unix.MSDOS_SUPER_MAGIC {
			err = f.Chmod(os.FileMode(value.Mode))
		}
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = unix.Renameat(fd, tmp, fd, base); err != nil {
		return err
	}
	return unix.Fsync(fd)
}
func inventory(root string) ([]File, error) {
	names := append([]string(nil), settings...)
	visited := 0
	var walk func(string) error
	walk = func(dir string) error {
		fd, base, err := parent(root, dir, false)
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
		f := os.NewFile(uintptr(n), dir)
		entries, err := f.ReadDir(MaxFiles + 1)
		f.Close()
		if err != nil && err != io.EOF {
			return err
		}
		if len(entries) > MaxFiles {
			return errors.New("too many settings files")
		}
		for _, entry := range entries {
			visited++
			if visited > MaxFiles {
				return errors.New("too many settings entries")
			}
			name := dir + "/" + entry.Name()
			if entry.IsDir() {
				if strings.Count(name, "/") > 12 {
					return errors.New("settings tree too deep")
				}
				if err = walk(name); err != nil {
					return err
				}
			} else {
				names = append(names, name)
			}
			if len(names) > MaxFiles {
				return errors.New("too many settings files")
			}
		}
		return nil
	}
	for _, dir := range trees {
		if err := walk(dir); err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
	}
	sort.Strings(names)
	files := []File{}
	total := 0
	for _, name := range names {
		f, err := read(root, name, MaxFile)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if f != nil {
			total += len(f.Data)
			if total > MaxData {
				return nil, errors.New("settings exceed archive limit")
			}
			files = append(files, *f)
		}
	}
	return files, nil
}

func retirePending(root string) error {
	fd, base, err := parent(root, PendingPath, false)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err = unix.Renameat(fd, base, fd, "failed.json"); err != nil {
		return err
	}
	return unix.Fsync(fd)
}
