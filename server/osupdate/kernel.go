package osupdate

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"syscall"
)

const kernelBootPath = "rootfs/boot/boot.sd"

var kernelReleaseRE = regexp.MustCompile(`^[0-9][0-9A-Za-z._+-]{0,95}$`)

// Format 3 replaces one boot FIT, with modules for a new, unique uname release.
// It never writes the bootloader, partition table or boot configuration.
type KernelUpdate struct {
	Release    string `json:"release"`
	SystemBase string `json:"system_base"`
}

func validKernelEntry(m Manifest, e Entry) bool {
	if m.Format != 3 || m.Kernel == nil || e.Link != "" || e.Preserve || e.Mode != 0644 || path.Clean(e.Path) != e.Path || strings.ContainsAny(e.Path, "\\\x00\r\n") {
		return false
	}
	return e.Path == kernelBootPath || strings.HasPrefix(e.Path, "rootfs/usr/lib/modules/"+m.Kernel.Release+"/")
}
func validateKernelManifest(m Manifest) error {
	if m.Format != 3 {
		if m.Kernel != nil {
			return errors.New("kernel metadata requires format 3")
		}
		return nil
	}
	if m.Kernel == nil || !kernelReleaseRE.MatchString(m.Kernel.Release) || !digestRE.MatchString(m.Kernel.SystemBase) {
		return errors.New("invalid kernel metadata")
	}
	boot, dep, builtin := false, false, false
	prefix := "rootfs/usr/lib/modules/" + m.Kernel.Release + "/"
	for _, e := range m.Files {
		if e.Path == kernelBootPath {
			boot = e.Size >= 40
		}
		if e.Path == prefix+"modules.dep" {
			dep = true
		}
		if e.Path == prefix+"modules.builtin" {
			builtin = true
		}
	}
	if !boot || !dep || !builtin {
		return errors.New("kernel package requires boot.sd and matching module metadata")
	}
	return nil
}
func (u *systemUpdater) checkKernelTarget(m Manifest) error {
	if m.Kernel == nil {
		return nil
	}
	current, err := os.ReadFile(u.at("/proc/sys/kernel/osrelease"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(current)) == m.Kernel.Release {
		return errors.New("kernel package must use a new unique kernel release")
	}
	// /boot must be an actual writable FAT filesystem, not a directory on rootfs.
	info, err := os.Lstat(u.at("/boot"))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid boot mount")
	}
	var fs syscall.Statfs_t
	if err = syscall.Statfs(u.at("/boot"), &fs); err != nil {
		return err
	}
	if fs.Type != 0x4d44 || fs.Flags&1 != 0 {
		return errors.New("kernel updates require the writable FAT boot partition")
	}
	old, err := os.Lstat(u.at("/boot/boot.sd"))
	if err != nil || !old.Mode().IsRegular() {
		return errors.New("missing regular boot.sd")
	}
	// One temporary FIT plus a small allocation/directory margin. The stock
	// 16 MiB boot partition cannot reserve an additional 4 MiB.
	var size int64
	for _, e := range m.Files {
		if e.Path == kernelBootPath {
			size = e.Size
		}
	}
	if size <= 0 || uint64(size+(64<<10)) > fs.Bavail*uint64(fs.Bsize) {
		return errors.New("insufficient boot partition space")
	}
	if exists(u.at("/usr/lib/modules/" + m.Kernel.Release)) {
		return errors.New("target kernel module directory already exists")
	}
	return nil
}

func checkFIT(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	var h [40]byte
	if _, err = io.ReadFull(f, h[:]); err != nil {
		return err
	}
	total := uint64(binary.BigEndian.Uint32(h[4:8]))
	if binary.BigEndian.Uint32(h[:4]) != 0xd00dfeed || total < 40 || total > uint64(info.Size()) {
		return errors.New("invalid boot FIT header")
	}
	// FIT is an FDT. Require structure and string blocks to fit inside its header size.
	for _, offsets := range [][2]int{{8, 36}, {12, 32}} {
		start := uint64(binary.BigEndian.Uint32(h[offsets[0] : offsets[0]+4]))
		size := uint64(binary.BigEndian.Uint32(h[offsets[1] : offsets[1]+4]))
		if start < 40 || size == 0 || start+size > total {
			return errors.New("invalid boot FIT bounds")
		}
	}
	return nil
}
func (u *systemUpdater) publishKernel(tx systemTransaction) (err error) {
	source := u.file("system-stage/" + kernelBootPath)
	if err = checkFIT(source); err != nil {
		_ = u.finish()
		return err
	}
	// A unique release lets us install modules beside the running kernel safely.
	// S00kmod precedes S00nkos-system-update on existing images, so the new
	// module tree must already be complete when the new FIT first boots.
	prefix := "rootfs/usr/lib/modules/" + tx.Kernel.Release + "/"
	for _, e := range tx.PackageFiles {
		if !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		dest := u.at(strings.TrimPrefix(e.Path, "rootfs"))
		if err = u.parent(dest, true); err != nil {
			return err
		}
		if err = replaceLinked(u.file("system-stage/"+e.Path), dest); err != nil {
			return err
		}
	}
	if err = syncTreeDirs(u.at("/usr/lib/modules/" + tx.Kernel.Release)); err != nil {
		return err
	}
	dest := u.at("/boot/boot.sd")
	pending := u.at("/boot/boot.sd.nkos-next")
	// The journal is already durable. On FAT we copy and fsync, never hard-link.
	published := false
	defer func() {
		if err != nil && !published {
			_ = os.Remove(pending)
			_ = u.finish()
		}
	}()
	if err = os.Remove(pending); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = copySync(source, pending, 0644); err != nil {
		return err
	}
	got, err := HashFile(pending)
	if err != nil {
		return err
	}
	want := ""
	for _, e := range tx.PackageFiles {
		if e.Path == kernelBootPath {
			want = e.SHA256
		}
	}
	if got != want {
		return errors.New("boot partition read-back checksum mismatch")
	}
	if err = os.Rename(pending, dest); err != nil {
		return err
	}
	published = true
	if err = syncDir(u.at("/boot")); err != nil {
		return err
	}
	return nil
}
func (u *systemUpdater) checkBootedKernel(tx systemTransaction) error {
	current, err := os.ReadFile(u.at("/proc/sys/kernel/osrelease"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(current)) != tx.Kernel.Release {
		return fmt.Errorf("expected kernel %s did not boot; full SD recovery required", tx.Kernel.Release)
	}
	if u.mountBoot != nil {
		if err = u.mountBoot(); err != nil {
			return fmt.Errorf("mount boot partition: %w", err)
		}
	}
	actual, err := HashFile(u.at("/boot/boot.sd"))
	if err != nil {
		return err
	}
	for _, e := range tx.PackageFiles {
		if e.Path == kernelBootPath && actual == e.SHA256 {
			return nil
		}
	}
	return errors.New("boot image changed after kernel update")
}

// The system update dispatcher runs before S01fs. The initramfs unmounts its
// temporary /boot before switch_root, so kernel verification must mount it.
func mountBootPartition() error {
	info, err := os.Lstat("/boot")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid boot mount point")
	}
	var fs syscall.Statfs_t
	if err = syscall.Statfs("/boot", &fs); err != nil {
		return err
	}
	if fs.Type != 0x4d44 {
		if err = syscall.Mount("/dev/mmcblk0p1", "/boot", "vfat", 0, ""); err != nil {
			return err
		}
		if err = syscall.Statfs("/boot", &fs); err != nil {
			return err
		}
	}
	if fs.Type != 0x4d44 || fs.Flags&1 != 0 {
		return errors.New("boot partition is not writable FAT")
	}
	return nil
}
