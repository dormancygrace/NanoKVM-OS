package standby

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type Mount struct{ Point, Source, Type, Options, SuperOptions string }

func hasOption(options, option string) bool {
	for _, s := range strings.Split(options, ",") {
		if s == option {
			return true
		}
	}
	return false
}
func unescapeMount(s string) string {
	return strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\").Replace(s)
}
func ParseMounts(data string) ([]Mount, error) {
	var out []Mount
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		f := strings.Fields(line)
		sep := -1
		for i, s := range f {
			if s == "-" {
				sep = i
				break
			}
		}
		if len(f) < 10 || sep < 6 || len(f) < sep+4 {
			return nil, errors.New("invalid mountinfo")
		}
		out = append(out, Mount{unescapeMount(f[4]), unescapeMount(f[sep+2]), f[sep+1], f[5], f[sep+3]})
	}
	return out, nil
}
func ReadMounts() ([]Mount, error) {
	b, e := os.ReadFile("/proc/self/mountinfo")
	if e != nil {
		return nil, e
	}
	return ParseMounts(string(b))
}
func memoryFS(t string) bool {
	switch t {
	case "tmpfs", "proc", "sysfs", "devtmpfs", "devpts", "configfs", "cgroup", "cgroup2", "mqueue", "securityfs", "debugfs", "tracefs", "pstore", "bpf":
		return true
	}
	return false
}
func blockFS(t string) bool {
	switch t {
	case "ext4", "f2fs", "vfat", "exfat", "ext2", "ext3", "squashfs", "iso9660":
		return true
	}
	return false
}
func syncFilesystems(mounts []Mount) error {
	for _, m := range mounts {
		if !blockFS(m.Type) {
			continue
		}
		fd, e := unix.Open(m.Point, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		e = unix.Syncfs(fd)
		unix.Close(fd)
		if e != nil {
			return fmt.Errorf("syncfs %s: %w", m.Point, e)
		}
	}
	return nil
}
func stopSwap() error {
	b, e := os.ReadFile("/proc/swaps")
	if e != nil {
		return e
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) < 5 {
			return errors.New("invalid swap table")
		}
		name, err := unix.BytePtrFromString(unescapeMount(f[0]))
		if err != nil {
			return err
		}
		_, _, errno := unix.Syscall(unix.SYS_SWAPOFF, uintptr(unsafe.Pointer(name)), 0, 0)
		if e = errno; errno != 0 {
			return fmt.Errorf("swapoff %s: %w", f[0], e)
		}
	}
	b, e = os.ReadFile("/proc/swaps")
	if e != nil {
		return e
	}
	if len(strings.Fields(string(b))) != 5 {
		return errors.New("swap remains active")
	}
	devices, e := filepath.Glob("/sys/block/zram*/reset")
	if e != nil {
		return e
	}
	for _, p := range devices {
		if e = os.WriteFile(p, []byte("1\n"), 0600); e != nil {
			return e
		}
	}
	return nil
}
func detachUSB() error {
	paths, e := filepath.Glob("/sys/kernel/config/usb_gadget/*/UDC")
	if e != nil {
		return e
	}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(b)) != "" {
			if e = os.WriteFile(p, []byte("\n"), 0600); e != nil {
				return fmt.Errorf("USB detach: %w", e)
			}
		}
	}
	paths, e = filepath.Glob("/sys/kernel/config/usb_gadget/*/functions/mass_storage.*/lun.*/file")
	if e != nil {
		return e
	}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(b)) != "" {
			if e = os.WriteFile(p, []byte("\n"), 0600); e != nil {
				return fmt.Errorf("USB backing file release: %w", e)
			}
			b, e = os.ReadFile(p)
			if e != nil || strings.TrimSpace(string(b)) != "" {
				return errors.New("USB storage is still attached")
			}
		}
	}
	return nil
}
func noUserProcesses() error {
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || pid == 1 {
			continue
		}
		b, e := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		stopped, e := processReleased(string(b))
		if e != nil {
			return e
		}
		if !stopped {
			return fmt.Errorf("process %d did not stop; storage may still be in use", pid)
		}
	}
	return nil
}
func detachLoops(prefix string) error {
	paths, e := filepath.Glob(prefix + "/sys/block/loop*/loop/backing_file")
	if e != nil {
		return e
	}
	for _, p := range paths {
		name := filepath.Base(filepath.Dir(filepath.Dir(p)))
		fd, e := unix.Open(prefix+"/dev/"+name, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.LOOP_CLR_FD, 0)
		unix.Close(fd)
		if errno != 0 {
			return fmt.Errorf("release %s: %w", name, errno)
		}
	}
	return nil
}

// No lazy/forced unmount. Busy mmap, loops, gadgets and D-state tasks fail closed.
func leaveStorage() error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var status unix.WaitStatus
		for {
			pid, _ := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if pid <= 0 {
				break
			}
		}
		if e := noUserProcesses(); e == nil {
			break
		} else if time.Now().After(deadline) {
			return e
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e := detachUSB(); e != nil {
		return e
	}
	if e := stopSwap(); e != nil {
		return e
	}
	mounts, e := ReadMounts()
	if e != nil {
		return e
	}
	if e = syncFilesystems(mounts); e != nil {
		return e
	}
	if e = unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); e != nil {
		return e
	}
	if e = os.Mkdir("/run/nkos-oldroot", 0700); e != nil {
		return e
	}
	if e = unix.PivotRoot("/run", "/run/nkos-oldroot"); e != nil {
		return e
	}
	if e = os.Chdir("/"); e != nil {
		return e
	}
	for _, name := range []string{"dev", "sys", "proc"} {
		if e = unix.Mount("/nkos-oldroot/"+name, "/nkos-standby/root/"+name, "", unix.MS_MOVE, ""); e != nil {
			return e
		}
	}
	b, e := os.ReadFile("/nkos-standby/root/proc/self/mountinfo")
	if e != nil {
		return e
	}
	mounts, e = ParseMounts(string(b))
	if e != nil {
		return e
	}
	sort.SliceStable(mounts, func(i, j int) bool { return len(mounts[i].Point) > len(mounts[j].Point) })
	// LOOP_CLR_FD marks mounted loops autoclear on supported Linux kernels.
	// Their backing files are released when the corresponding mounts close.
	if e = detachLoops("/nkos-standby/root"); e != nil {
		return e
	}
	var pending []Mount
	for _, m := range mounts {
		if m.Point == "/nkos-oldroot" || strings.HasPrefix(m.Point, "/nkos-oldroot/") {
			pending = append(pending, m)
		}
	}
	// A loop mounted at /m may hold a backing file on /data. Depth order alone
	// cannot express that dependency: retry EBUSY only after another mount
	// actually closed. Never use MNT_DETACH or MNT_FORCE.
	for len(pending) > 0 {
		var busy []Mount
		for _, m := range pending {
			e = unix.Unmount(m.Point, 0)
			if errors.Is(e, unix.EBUSY) {
				busy = append(busy, m)
				continue
			}
			if e != nil {
				return fmt.Errorf("unmount %s: %w", m.Point, e)
			}
		}
		if len(busy) == len(pending) {
			return fmt.Errorf("unmount %s: %w", busy[0].Point, unix.EBUSY)
		}
		pending = busy
	}
	// Check the entire post-pivot namespace BEFORE chroot hides mounts outside
	// the helper's subtree (for example a persistent bind below the old /run).
	b, e = os.ReadFile("/nkos-standby/root/proc/self/mountinfo")
	if e != nil {
		return e
	}
	mounts, e = ParseMounts(string(b))
	if e != nil {
		return e
	}
	for _, m := range mounts {
		if !memoryFS(m.Type) {
			return fmt.Errorf("persistent filesystem remains mounted at %s", m.Point)
		}
	}

	return nil
}

type Counters struct{ Writes, Written, Discards, Discarded, Flushes, InFlight uint64 }

func ParseCounters(data string) (Counters, error) {
	f := strings.Fields(data)
	if len(f) < 17 {
		return Counters{}, errors.New("block statistics lack discard/flush accounting")
	}
	var c Counters
	for i, p := range map[int]*uint64{4: &c.Writes, 6: &c.Written, 8: &c.InFlight, 11: &c.Discards, 13: &c.Discarded, 15: &c.Flushes} {
		n, e := strconv.ParseUint(f[i], 10, 64)
		if e != nil {
			return c, e
		}
		*p = n
	}
	return c, nil
}

type Disk struct {
	Name     string
	Baseline Counters
}

func protectDisks() ([]Disk, error) {
	// Never block writeback on a filesystem that was not released: a failed
	// transition must still be able to sync and reboot safely.
	if err := memoryMountsOnly(); err != nil {
		return nil, err
	}
	entries, e := os.ReadDir("/sys/block")
	if e != nil {
		return nil, e
	}
	var disks []Disk
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") {
			continue
		}
		if strings.HasPrefix(name, "dm-") {
			return nil, errors.New("device-mapper standby is unsupported")
		}
		// Protect partitions too: raw partition descriptors must not bypass
		// the whole-disk read-only flag.
		parts, e := filepath.Glob("/sys/block/" + name + "/" + name + "*")
		if e != nil {
			return nil, e
		}
		nodes := []string{name}
		for _, p := range parts {
			if _, e = os.Stat(p + "/partition"); e == nil {
				nodes = append(nodes, filepath.Base(p))
			}
		}
		for _, node := range nodes {
			fd, e := unix.Open("/dev/"+node, unix.O_RDONLY|unix.O_CLOEXEC, 0)
			if e != nil {
				return nil, e
			}
			_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.BLKFLSBUF, 0)
			if errno != 0 {
				unix.Close(fd)
				return nil, fmt.Errorf("flush %s: %w", node, errno)
			}
			e = unix.IoctlSetPointerInt(fd, unix.BLKROSET, 1)
			if e == nil {
				ro, err := unix.IoctlGetInt(fd, unix.BLKROGET)
				e = err
				if e == nil && ro != 1 {
					e = errors.New("block device remains writable")
				}
			}
			unix.Close(fd)
			if e != nil {
				return nil, e
			}
		}
		disks = append(disks, Disk{Name: name})
	}
	if len(disks) == 0 {
		return nil, errors.New("no physical storage found to verify")
	}
	for i := range disks {
		b, e := os.ReadFile("/sys/block/" + disks[i].Name + "/stat")
		if e != nil {
			return nil, e
		}
		disks[i].Baseline, e = ParseCounters(string(b))
		if e != nil {
			return nil, e
		}
		if disks[i].Baseline.InFlight != 0 {
			return nil, errors.New("block I/O remains in flight")
		}
	}
	return disks, nil
}
func verifyStorage(disks []Disk) error {
	if err := watchdogsStopped(); err != nil {
		return err
	}
	if e := memoryMountsOnly(); e != nil {
		return e
	}
	for _, d := range disks {
		b, e := os.ReadFile("/sys/block/" + d.Name + "/ro")
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(b)) != "1" {
			return fmt.Errorf("%s is no longer read-only", d.Name)
		}
		b, e = os.ReadFile("/sys/block/" + d.Name + "/stat")
		if e != nil {
			return e
		}
		c, e := ParseCounters(string(b))
		if e != nil {
			return e
		}
		if c != d.Baseline || c.InFlight != 0 {
			return fmt.Errorf("I/O changed after storage release on %s", d.Name)
		}
	}
	return nil
}

func memoryMountsOnly() error {
	mounts, err := ReadMounts()
	if err != nil {
		return err
	}
	for _, m := range mounts {
		if !memoryFS(m.Type) {
			return fmt.Errorf("persistent filesystem remains mounted at %s", m.Point)
		}
	}
	return nil
}

// /proc/PID/exe can disappear before a dying process releases every file.
// Inspect task state and PF_KTHREAD instead of treating a readlink error as
// proof that the process is harmless. Reaped zombies have no files or mm.
func processReleased(stat string) (bool, error) {
	end := strings.LastIndex(stat, ")")
	if end < 0 {
		return false, errors.New("invalid process stat")
	}
	f := strings.Fields(stat[end+1:])
	if len(f) < 7 {
		return false, errors.New("truncated process stat")
	}
	flags, err := strconv.ParseUint(f[6], 10, 64)
	if err != nil {
		return false, err
	}
	return f[0] == "Z" || f[0] == "X" || flags&0x00200000 != 0, nil
}
