package osupdate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// System packages deliberately exclude the boot partition, kernel/modules,
// libc/loader, accounts, networking credentials and the stable boot dispatcher.
// Those require a separate boot/recovery design, not an unrestricted tar extract.
func systemPathAllowed(name string) bool {
	if path.Clean(name) != name || !strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00\r\n") {
		return false
	}
	for _, p := range []string{"/usr/bin/", "/usr/sbin/", "/usr/lib/", "/usr/share/", "/usr/libexec/"} {
		if strings.HasPrefix(name, p) {
			if strings.HasPrefix(name, "/usr/lib/modules/") || strings.HasPrefix(name, "/usr/lib/firmware/") || strings.HasPrefix(path.Base(name), "ld-musl-") || path.Base(name) == "libc.so" {
				return false
			}
			return true
		}
	}
	if strings.HasPrefix(name, "/etc/mc/") {
		return true
	}
	switch name {
	case "/etc/chrony.conf", "/etc/ntp.conf", "/etc/nanorc":
		return true
	}
	// Service scripts run only on the next boot, after system installation.
	if strings.HasPrefix(name, "/etc/init.d/") && path.Dir(name) == "/etc/init.d" {
		base := path.Base(name)
		return len(base) > 3 && base[0] == 'S' && base[1] >= '1' && base[1] <= '9' && base[2] >= '0' && base[2] <= '9' && base != "S94nanokvm-update" && base != "S99nkos-system-confirm"
	}
	return false
}
func validSystemRemoval(name string) bool {
	return name != "rootfs"+Helper && validSystemEntry(Entry{Path: name, Mode: 0644})
}
func validSystemEntry(e Entry) bool {
	if !strings.HasPrefix(e.Path, "rootfs/") || !systemPathAllowed(strings.TrimPrefix(e.Path, "rootfs")) || (e.Mode != 0644 && e.Mode != 0755) {
		return false
	}
	dest := strings.TrimPrefix(e.Path, "rootfs")
	config := strings.HasPrefix(dest, "/etc/") && !strings.HasPrefix(dest, "/etc/init.d/")
	if config != e.Preserve {
		return false
	}
	if e.Link != "" {
		hash := sha256.Sum256([]byte(e.Link))
		if hex.EncodeToString(hash[:]) != e.SHA256 {
			return false
		}
		if e.Preserve || e.Mode != 0644 || e.Size != int64(len(e.Link)) || strings.ContainsAny(e.Link, "\x00\r\n\\") || len(e.Link) > 1024 {
			return false
		}
		target := e.Link
		if !path.IsAbs(target) {
			target = path.Join(path.Dir(dest), target)
		}
		if !systemPathAllowed(target) {
			return false
		}
	}
	return true
}

type savedSystemFile struct {
	Delete  bool   `json:"delete,omitempty"`
	Entry   Entry  `json:"entry"`
	Existed bool   `json:"existed"`
	Link    string `json:"old_link,omitempty"`
	Skip    bool   `json:"skip,omitempty"`
}
type systemTransaction struct {
	Kernel       *KernelUpdate     `json:"kernel,omitempty"`
	PackageFiles []Entry           `json:"package_files"`
	Phase        string            `json:"phase"`
	ID           string            `json:"id"`
	Version      string            `json:"version"`
	Sequence     uint64            `json:"sequence"`
	OldInstalled Installed         `json:"old_installed"`
	OldVersion   string            `json:"old_version"`
	BootID       string            `json:"boot_id"`
	Files        []savedSystemFile `json:"files"`
}

// Root is injectable for crash/recovery tests. Production always uses /.
type systemUpdater struct {
	root      string
	bootID    func() string
	mountBoot func() error
}

func newSystemUpdater() *systemUpdater {
	return &systemUpdater{root: "/", mountBoot: mountBootPartition, bootID: func() string {
		b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
		return strings.TrimSpace(string(b))
	}}
}
func (u *systemUpdater) at(p string) string              { return filepath.Join(u.root, p) }
func (u *systemUpdater) file(p string) string            { return u.at(Base + "/" + p) }
func (u *systemUpdater) save(tx systemTransaction) error { return writeJSON(u.file("system.json"), tx) }
func (u *systemUpdater) read() (systemTransaction, error) {
	var tx systemTransaction
	err := readJSON(u.file("system.json"), &tx)
	return tx, err
}
func (u *systemUpdater) result(r Result) error { return writeJSON(u.file("result.json"), r) }

// Never follow a symlink or another mount while reaching a managed destination.
func (u *systemUpdater) parent(dest string, create bool) error {
	rel, err := filepath.Rel(u.root, filepath.Dir(dest))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return errors.New("destination escaped root")
	}
	var rootStat syscall.Stat_t
	if err = syscall.Stat(u.root, &rootStat); err != nil {
		return err
	}
	current := u.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if os.IsNotExist(e) {
			if !create {
				return nil
			}
			if e = os.Mkdir(current, 0755); e != nil {
				return e
			}
			if e = syncDir(filepath.Dir(current)); e != nil {
				return e
			}
			info, e = os.Lstat(current)
		}
		if e != nil {
			return e
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || !ok || st.Dev != rootStat.Dev {
			return fmt.Errorf("unsafe system parent: %s", current)
		}
	}
	return nil
}
func copySync(from, to string, mode os.FileMode) (err error) {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		dst.Close()
		if err != nil {
			os.Remove(to)
		}
	}()
	if _, err = io.Copy(dst, src); err != nil {
		return err
	}
	if err = dst.Chmod(mode); err != nil {
		return err
	}
	return dst.Sync()
}

// The original inode is retained for rollback. Atomic replacement never writes
// through it, so backup and recovery work for open executables and shared libs.
func replaceLinked(from, to string) error {
	tmp := to + ".nkos-next"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(from, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, to); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(to))
}
func replaceSymlink(link, to string) error {
	tmp := to + ".nkos-next"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(link, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, to); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(to))
}
func (u *systemUpdater) stage(file string, b *Bundle) error {
	if exists(u.file("system.json")) || exists(u.file("transaction.json")) {
		return errors.New("an update needs recovery")
	}
	// Leave room for extraction, a largest-file replacement, metadata and logs.
	var fs syscall.Statfs_t
	if err := syscall.Statfs(u.root, &fs); err != nil {
		return err
	}
	var total int64
	for _, e := range b.Manifest.Files {
		total += e.Size
	}
	if uint64(total+(64<<20)) > fs.Bavail*uint64(fs.Bsize) {
		return errors.New("insufficient free root filesystem space for system update")
	}
	for _, name := range []string{"system-stage", "system-backup", "system-app-previous", "system-app-failed", "system-payload"} {
		if err := os.RemoveAll(u.file(name)); err != nil {
			return err
		}
	}
	if err := Extract(file, b, u.file("system-stage")); err != nil {
		return err
	}
	app := u.file("system-stage")
	if err := os.Mkdir(app+"/dl_lib", 0755); err != nil {
		return err
	}
	libs, err := os.ReadDir(u.at("/kvmapp/server/dl_lib"))
	if err != nil {
		return err
	}
	for _, lib := range libs {
		if err = os.Link(u.at("/kvmapp/server/dl_lib/"+lib.Name()), app+"/dl_lib/"+lib.Name()); err != nil {
			return err
		}
	}
	tx := systemTransaction{Kernel: b.Manifest.Kernel, Phase: "ready", ID: b.ID, Version: b.Manifest.Version, Sequence: b.Manifest.Sequence, PackageFiles: b.Manifest.Files}
	_ = readJSON(u.file("installed.json"), &tx.OldInstalled)
	old, err := os.ReadFile(u.at("/kvmapp/version"))
	if err != nil {
		return err
	}
	tx.OldVersion = string(old)
	entries := append([]Entry(nil), b.Manifest.Files...)
	removed := map[string]bool{}
	for _, name := range b.Manifest.Remove {
		entries = append(entries, Entry{Path: name, Mode: 0644})
		removed[name] = true
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Path, "rootfs/") {
			continue
		}
		if !validSystemEntry(e) && !validKernelEntry(b.Manifest, e) {
			return errors.New("invalid system entry")
		}
		if e.Path == kernelBootPath {
			continue
		}
		dest := u.at(strings.TrimPrefix(e.Path, "rootfs"))
		if err = u.parent(dest, false); err != nil {
			return err
		}
		info, statErr := os.Lstat(dest)
		f := savedSystemFile{Entry: e, Existed: statErr == nil, Delete: removed[e.Path]}
		if f.Delete && !f.Existed {
			f.Skip = true
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if f.Existed {
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("unsupported existing system file: %s", e.Path)
			}
			if e.Preserve {
				f.Skip = true
			} else if info.Mode()&os.ModeSymlink != 0 {
				f.Link, err = os.Readlink(dest)
				if err != nil {
					return err
				}
			} else {
				backup := u.file("system-backup/" + e.Path)
				if err = os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
					return err
				}
				// Copy, rather than link: the running service could still modify a file
				// before reboot. The saved snapshot must remain immutable.
				if err = copySync(dest, backup, info.Mode().Perm()); err != nil {
					return err
				}
			}
		}
		if e.Link != "" {
			data, err := os.ReadFile(u.file("system-stage/" + e.Path))
			if err != nil {
				return err
			}
			if !bytes.Equal(data, []byte(e.Link)) {
				return errors.New("symbolic link payload mismatch")
			}
		}
		tx.Files = append(tx.Files, f)
	}
	if len(tx.Files) == 0 {
		return errors.New("system package contains no system files")
	}
	// The old statically linked helper remains available even if the package
	// updates /usr/sbin/nkos-update itself. The fixed dispatcher chooses this copy.
	helper := u.file("system-recovery.new")
	os.Remove(helper)
	if err = copySync(u.at(Helper), helper, 0700); err != nil {
		return err
	}
	if err = os.Rename(helper, u.file("system-recovery")); err != nil {
		return err
	}
	for _, d := range []string{"system-stage", "system-backup"} {
		if exists(u.file(d)) {
			if err = syncTreeDirs(u.file(d)); err != nil {
				return err
			}
		}
	}
	if err = syncDir(u.file("")); err != nil {
		return err
	}
	if err = u.save(tx); err != nil {
		return err
	}
	if tx.Kernel != nil {
		return u.publishKernel(tx)
	}
	return nil
}
func (u *systemUpdater) restore(tx systemTransaction) error {
	if tx.Kernel != nil {
		return errors.New("kernel update interrupted; automatic rollback is unavailable; recover with a full SD image")
	}
	for i := len(tx.Files) - 1; i >= 0; i-- {
		f := tx.Files[i]
		if f.Skip {
			continue
		}
		dest := u.at(strings.TrimPrefix(f.Entry.Path, "rootfs"))
		if err := u.parent(dest, true); err != nil {
			return err
		}
		var err error
		if !f.Existed {
			err = os.Remove(dest)
			if os.IsNotExist(err) {
				err = nil
			}
		} else if f.Link != "" {
			err = replaceSymlink(f.Link, dest)
		} else {
			err = replaceLinked(u.file("system-backup/"+f.Entry.Path), dest)
		}
		if err != nil {
			return err
		}
		if err = syncDir(filepath.Dir(dest)); err != nil {
			return err
		}
	}
	previous := u.file("system-app-previous")
	if exists(previous) {
		if exists(u.at("/kvmapp/server")) {
			if err := os.RemoveAll(u.file("system-app-failed")); err != nil {
				return err
			}
			if err := os.Rename(u.at("/kvmapp/server"), u.file("system-app-failed")); err != nil {
				return err
			}
		}
		if err := os.Rename(previous, u.at("/kvmapp/server")); err != nil {
			return err
		}
	}
	if err := writeAtomic(u.at("/kvmapp/version"), []byte(tx.OldVersion), 0644); err != nil {
		return err
	}
	if err := writeJSON(u.file("installed.json"), tx.OldInstalled); err != nil {
		return err
	}
	if err := syncDir(u.at("/kvmapp")); err != nil {
		return err
	}
	tx.Phase = "rolled-back"
	if err := u.save(tx); err != nil {
		return err
	}
	if err := u.result(Result{State: "rolled-back", Version: tx.Version, ID: tx.ID, Message: "System update was not confirmed; previous files restored"}); err != nil {
		return err
	}
	return u.finish()
}
func (u *systemUpdater) finish() error {
	if err := os.Remove(u.file("system.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDir(u.file(""))
}

// boot runs synchronously before other init scripts. An unconfirmed earlier
// attempt is rolled back; it is never blindly retried on every boot.
func (u *systemUpdater) boot() (err error) {
	tx, err := u.read()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if tx.Phase == "committed" || tx.Phase == "rolled-back" {
		return u.finish()
	}
	if tx.Kernel != nil {
		if err = u.checkBootedKernel(tx); err != nil {
			return err
		}
	}
	if tx.Phase != "ready" {
		return u.restore(tx)
	}
	tx.Phase = "applying"
	tx.BootID = u.bootID()
	if tx.BootID == "" {
		return errors.New("missing boot identifier")
	}
	if err = u.save(tx); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if re := u.restore(tx); re != nil {
				err = fmt.Errorf("%v; recovery failed: %w", err, re)
			} else {
				err = nil
			}
		}
	}()
	for _, e := range tx.PackageFiles {
		sum, checkErr := HashFile(u.file("system-stage/" + e.Path))
		if checkErr != nil {
			return checkErr
		}
		if sum != e.SHA256 {
			return errors.New("staged package checksum mismatch")
		}
	}
	for _, f := range tx.Files {
		if f.Skip {
			continue
		}
		dest := u.at(strings.TrimPrefix(f.Entry.Path, "rootfs"))
		if err = u.parent(dest, true); err != nil {
			return err
		}
		if f.Delete {
			err = os.Remove(dest)
			if os.IsNotExist(err) {
				err = nil
			}
			if err == nil {
				err = syncDir(filepath.Dir(dest))
			}
		} else if f.Entry.Link != "" {
			err = replaceSymlink(f.Entry.Link, dest)
		} else {
			err = replaceLinked(u.file("system-stage/"+f.Entry.Path), dest)
		}
		if err != nil {
			return err
		}
	}
	// Keep the system payload outside the application tree.
	if exists(u.file("system-stage/rootfs")) {
		if err = os.Rename(u.file("system-stage/rootfs"), u.file("system-payload")); err != nil {
			return err
		}
	}
	if err = os.Rename(u.at("/kvmapp/server"), u.file("system-app-previous")); err != nil {
		return err
	}
	if err = syncDir(u.at("/kvmapp")); err != nil {
		return err
	}
	if err = syncDir(u.file("")); err != nil {
		return err
	}
	if err = os.Rename(u.file("system-stage"), u.at("/kvmapp/server")); err != nil {
		return err
	}
	if err = syncDir(u.at("/kvmapp")); err != nil {
		return err
	}
	if err = syncDir(u.file("")); err != nil {
		return err
	}
	tx.Phase = "testing"
	if err = u.save(tx); err != nil {
		return err
	}
	return u.result(Result{State: "installing", Version: tx.Version, ID: tx.ID, Reboot: true, Message: "System files installed; checking startup"})
}
func (u *systemUpdater) confirm(check func() bool) error {
	tx, err := u.read()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if tx.Phase != "testing" || tx.BootID != u.bootID() {
		return errors.New("no system update to confirm in this boot")
	}
	if !check() {
		return errors.New("new system/application did not become healthy; reboot to restore previous files")
	}
	if tx.Kernel != nil {
		if err = writeAtomic(u.at("/etc/nkos-system-base"), []byte(tx.Kernel.SystemBase+"\n"), 0644); err != nil {
			return err
		}
	}
	if err = writeAtomic(u.at("/kvmapp/version"), []byte(tx.Version+"\n"), 0644); err != nil {
		return err
	}
	if err = writeJSON(u.file("installed.json"), Installed{Version: tx.Version, Sequence: tx.Sequence}); err != nil {
		return err
	}
	tx.Phase = "committed"
	if err = u.save(tx); err != nil {
		return err
	}
	if err = u.result(Result{State: "installed", Version: tx.Version, ID: tx.ID, Message: "System and application update installed"}); err != nil {
		return err
	}
	return u.finish()
}
func SystemBoot() error { return newSystemUpdater().boot() }
func ConfirmSystem() error {
	u := newSystemUpdater()
	err := u.confirm(func() bool {
		consecutive := 0
		for i := 0; i < 90; i++ {
			time.Sleep(time.Second)
			if healthy() {
				consecutive++
			} else {
				consecutive = 0
			}
			if consecutive >= 5 {
				return true
			}
		}
		return false
	})
	if err != nil {
		_ = u.result(Result{State: "failed", Message: err.Error(), Reboot: true})
	}
	return err
}

// JSON is used instead of arbitrary post-install scripts. Dependencies are
// assembled and signed by the release builder, not fetched during installation.
