package standby

import (
	"crypto/rand"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"NanoKVM-Server/internal/networktrial"

	"golang.org/x/sys/unix"
)

// Prepared owns the cross-process exclusion locks until init starts shutdown.
type Prepared struct {
	Config   Config
	locks    []*os.File
	transfer bool
	armed    bool
	bound    bool
}

func (p *Prepared) Close() {
	if p.armed || p.bound {
		return
	}
	for _, f := range p.locks {
		f.Close()
	}
	if p.transfer {
		os.Remove("/run/nanokvm-transfer-in-progress")
	}
	os.Remove(Marker)
	os.RemoveAll(Directory)
}

func lockFile(name string) (*os.File, error) {
	fd, err := unix.Open(name, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("not a regular lock file")
	}
	if err == nil {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("operation busy (%s): %w", name, err)
	}
	return f, nil
}

func Prepare(c Config) (_ *Prepared, result error) {
	// Admission is already closed. Refuse active/retrying background trials
	// and their shared on-disk record before any network snapshot or staging.
	if err := networktrial.CheckIdle(networktrial.RecordPath); err != nil {
		return nil, fmt.Errorf("finish network setup before shutdown: %w", err)
	}
	// Never arm this mechanism on systemd, a container, or a custom root layout.
	initInfo, err := os.Stat("/proc/1/exe")
	if err != nil {
		return nil, err
	}
	busyInfo, err := os.Stat("/bin/busybox")
	if err != nil || !os.SameFile(initInfo, busyInfo) {
		return nil, errors.New("standby requires NanoKVM BusyBox init")
	}
	table, err := os.ReadFile("/etc/inittab")
	if err != nil {
		return nil, err
	}
	if _, err = handoffInittab(string(table)); err != nil {
		return nil, err
	}
	mounts, err := ReadMounts()
	if err != nil {
		return nil, err
	}
	rootOK, ramOK := false, false
	for _, m := range mounts {
		if m.Point == "/" {
			rootOK = m.Source == "/dev/mmcblk0p2" && (m.Type == "ext4" || m.Type == "f2fs")
		}
		if m.Point == "/run" {
			ramOK = m.Type == "tmpfs" && !hasOption(m.Options, "noexec") && !hasOption(m.Options, "ro")
		}
		if !memoryFS(m.Type) && !blockFS(m.Type) {
			return nil, fmt.Errorf("unsupported mounted filesystem %s at %s", m.Type, m.Point)
		}
	}
	if !rootOK || !ramOK {
		return nil, errors.New("standby requires the standard SD root and executable /run tmpfs")
	}
	if _, err = os.Lstat(Directory); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("standby preparation already exists; reboot before retrying")
	}
	for _, name := range []string{"/var/lib/nanokvm-recovery/pending.json", "/boot/rec"} {
		if _, err = os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("finish recovery/network setup before shutdown (%s)", name)
		}
	}
	if err = snapshotNetwork(&c); err != nil {
		return nil, err
	}
	if err = os.Mkdir(Directory, 0700); err != nil {
		return nil, err
	}
	p := &Prepared{Config: c}
	defer func() {
		if result != nil {
			p.Close()
		}
	}()
	// Match osupdate's lock-directory creation on installations that have
	// never opened the updater. This is the existing lock location, not a
	// persistent standby setting.
	if err = os.MkdirAll("/kvmapp/.os-update", 0700); err != nil {
		return nil, err
	}
	for _, name := range []string{"/kvmapp/.os-update/lock", "/run/nanokvm-apk.lock", "/lib/apk/db/lock"} {
		f, e := lockFile(name)
		if e != nil {
			return nil, e
		}
		p.locks = append(p.locks, f)
	}
	f, err := os.OpenFile("/run/nanokvm-transfer-in-progress", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("an upload or image download is running")
	}
	f.Close()
	p.transfer = true
	// Closing the API gate and taking the locks precede this marker. Queued
	// package workers recheck it after acquiring their lock.
	if err = os.WriteFile(Marker, []byte("preparing\n"), 0600); err != nil {
		return nil, err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return nil, err
	}
	p.Config.Token = hex.EncodeToString(token)
	for _, dir := range []string{"bin", "lib", "usr/lib", "dev", "proc", "sys", "run", "tmp"} {
		if err = os.MkdirAll(filepath.Join(Root, dir), 0700); err != nil {
			return nil, err
		}
	}
	if err = staticELF(Helper); err != nil {
		return nil, err
	}
	// Discover the ELF closure and reserve RAM before copying any payload.
	planned := map[string]bool{}
	if err = stageELF("/bin/busybox", "", planned); err != nil {
		return nil, err
	}
	var wifiConfig []byte
	if c.WiFi {
		if err = stageELF("/sbin/wpa_supplicant", "", planned); err != nil {
			return nil, err
		}
		data, e := os.ReadFile("/run/nanokvm-wifi/wpa_supplicant.conf")
		if e != nil {
			return nil, e
		}
		if len(data) > 256<<10 {
			return nil, errors.New("Wi-Fi configuration is too large")
		}
		wifiConfig, e = ramWPA(data)
		if e != nil {
			return nil, e
		}
	}
	info, err := os.Stat(Helper)
	if err != nil {
		return nil, err
	}
	payloadBytes := info.Size()
	for name := range planned {
		info, e := os.Stat(name)
		if e != nil {
			return nil, e
		}
		payloadBytes += info.Size()
	}
	available, err := memAvailable()
	if err != nil {
		return nil, err
	}
	if err = checkRAMBudget(payloadBytes, available); err != nil {
		return nil, err
	}
	var space unix.Statfs_t
	if err = unix.Statfs("/run", &space); err != nil {
		return nil, err
	}
	if int64(space.Bavail)*space.Bsize < payloadBytes+4<<20 {
		return nil, errors.New("/run has insufficient free space")
	}
	if err = copyFile(Helper, Root+"/init", 0700); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	if err = stageELF("/bin/busybox", Root, seen); err != nil {
		return nil, err
	}
	if c.WiFi {
		if err = stageELF("/sbin/wpa_supplicant", Root, seen); err != nil {
			return nil, err
		}
		if err = os.WriteFile(Root+"/wpa.conf", wifiConfig, 0600); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(p.Config)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(Root+"/config.json", data, 0600); err != nil {
		return nil, err
	}
	cmd := exec.Command(Root+"/init", "probe", Root)
	if out, e := cmd.CombinedOutput(); e != nil {
		return nil, fmt.Errorf("RAM helper probe: %s: %w", out, e)
	}
	if err = syncFilesystems(mounts); err != nil {
		return nil, err
	}
	return p, nil
}

// Arm first gets acknowledgement from an init-owned respawn action. Sleeping
// for an assumed SIGHUP delay could otherwise turn SIGQUIT into a no-op.
func (p *Prepared) Arm() error {
	data, err := os.ReadFile("/etc/inittab")
	if err != nil {
		return err
	}
	table, err := handoffInittab(string(data))
	if err != nil {
		return err
	}
	if err = os.WriteFile(Directory+"/inittab", []byte(table), 0600); err != nil {
		return err
	}
	rc, err := os.ReadFile("/etc/rc.conf")
	if err != nil {
		return err
	}
	// Avoid '*' here: OpenRC expands this value unquoted, so /.* would
	// become /. and /.. instead of the intended mountinfo regular expression.
	rc = append(rc, []byte("\n# RAM-only standby handoff\nRC_NO_UMOUNTS=\"/:/.+\"\n")...)
	if err = os.WriteFile(Directory+"/rc.conf", rc, 0600); err != nil {
		return err
	}
	if err = unix.Mount(Directory+"/rc.conf", "/etc/rc.conf", "", unix.MS_BIND, ""); err != nil {
		return err
	}
	p.bound = true
	if err = unix.Mount(Directory+"/inittab", "/etc/inittab", "", unix.MS_BIND, ""); err != nil {
		if e := unix.Unmount("/etc/rc.conf", 0); e != nil {
			return fmt.Errorf("%w; cannot restore rc.conf (%v); reboot before retrying", err, e)
		}
		p.bound = false
		return err
	}
	rollback := func(cause error) error {
		if e := unix.Unmount("/etc/inittab", 0); e != nil {
			return fmt.Errorf("%w; cannot restore init (%v); reboot before retrying", cause, e)
		}
		if e := unix.Unmount("/etc/rc.conf", 0); e != nil {
			return fmt.Errorf("%w; cannot restore rc.conf (%v); reboot before retrying", cause, e)
		}
		if e := unix.Kill(1, unix.SIGHUP); e != nil {
			return fmt.Errorf("%w; cannot reload restored init (%v); reboot before retrying", cause, e)
		}
		p.bound = false
		return cause
	}
	if err = unix.Kill(1, unix.SIGHUP); err != nil {
		return rollback(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, e := os.ReadFile(Directory + "/ack"); e == nil && string(b) == p.Config.Token {
			break
		}
		if time.Now().After(deadline) {
			return rollback(errors.New("init did not acknowledge the RAM handoff"))
		}
		time.Sleep(25 * time.Millisecond)
	}
	// The ack worker waits for commit, keeping the original API alive long
	// enough to deliver its capability. It then asks init for the restart action.
	if err = os.WriteFile(Directory+"/commit", []byte("1"), 0600); err != nil {
		return rollback(err)
	}
	p.armed = true
	return nil
}

func handoffInittab(table string) (string, error) {
	if !strings.Contains(table, "::shutdown:/sbin/openrc shutdown") {
		return "", errors.New("custom init shutdown actions are unsupported")
	}
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, ":shutdown:") && line != "::shutdown:/sbin/openrc shutdown" {
			return "", errors.New("custom init shutdown actions are unsupported")
		}
		if strings.Contains(line, ":restart:") {
			return "", errors.New("custom init restart action is unsupported")
		}
	}
	table = strings.ReplaceAll(table, "::shutdown:/sbin/openrc shutdown", "::shutdown:"+Root+"/init shutdown")
	return table + "\n::restart:" + Root + "/init takeover\n::respawn:" + Root + "/init acknowledge\n", nil
}

func Acknowledge() error {
	if os.Getppid() != 1 {
		return errors.New("acknowledgement must be started by init")
	}
	c, err := LoadConfig(Root + "/config.json")
	if err != nil {
		return err
	}
	if err = os.WriteFile(Directory+"/ack", []byte(c.Token), 0600); err != nil {
		return err
	}
	for {
		if _, err = os.Stat(Directory + "/commit"); err == nil {
			break
		}
		if _, err = os.Stat(Marker); err != nil {
			return errors.New("handoff cancelled")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
	if err = unix.Kill(1, unix.SIGQUIT); err != nil {
		return err
	}
	for {
		time.Sleep(time.Hour)
	}
}

func LoadConfig(name string) (Config, error) {
	var c Config
	b, err := os.ReadFile(name)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err == nil && (len(c.Token) != 64 || net.ParseIP(c.Address).To4() == nil || c.Port < 1 || c.Port > 65535) {
		err = errors.New("invalid standby configuration")
	}
	return c, err
}

func staticELF(name string) error {
	f, err := elf.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return errors.New("standby helper must be statically linked")
		}
	}
	return nil
}
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return fmt.Errorf("unsupported RAM payload: %s", src)
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func memAvailable() (int64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 1 && f[0] == "MemAvailable:" {
			n, e := strconv.ParseInt(f[1], 10, 64)
			return n * 1024, e
		}
	}
	return 0, errors.New("cannot determine available RAM")
}

// Probe runs the copied executable, libc and networking utilities in a chroot,
// with no proc/dev/SD mounts. It never changes host network state.
func Probe(root string) error {
	if err := syscall.Chroot(root); err != nil {
		return err
	}
	if err := os.Chdir("/"); err != nil {
		return err
	}
	if _, err := LoadConfig("/config.json"); err != nil {
		return err
	}
	cmd := exec.Command("/bin/busybox", "true")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if c, err := LoadConfig("/config.json"); err != nil {
		return err
	} else if c.WiFi {
		cmd = exec.Command("/sbin/wpa_supplicant", "-v")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	return nil
}

// Pending is checked after package locks are acquired, including by queued helpers.
func Pending() error {
	if _, err := os.Stat(Marker); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return errors.New("NanoKVM shutdown is in progress")
}

func checkRAMBudget(payload, available int64) error {
	if payload < 0 || payload > 48<<20 || available < payload+24<<20 {
		return errors.New("not enough RAM for network standby; stop video/add-ons and retry")
	}
	return nil
}
