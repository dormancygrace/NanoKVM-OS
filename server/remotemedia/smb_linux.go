package remotemedia

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const smbHelperArg = "--nanokvm-smb-reader"
const (
	smbOK uint32 = iota
	smbUnavailable
	smbMountFailed
	smbOpenFailed
	smbChanged
	smbReadFailed
)

// A timed-out kernel syscall may take time to exit after SIGKILL. Do not launch
// more readers until the previous child has actually been reaped.
var smbReaderSlot = make(chan struct{}, 1)

func init() {
	if len(os.Args) == 2 && os.Args[1] == smbHelperArg {
		peer := os.NewFile(3, "smb-control")
		shared, err := unix.Mmap(4, 0, 2*MaxRead, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		unix.Close(4) // not close-on-exec: keep it from modprobe
		if peer == nil || err != nil {
			os.Exit(1)
		}
		err = serveSMBHelper(peer, shared, mountSMBFile)
		peer.Close()
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func smbStatusError(status uint32) error {
	switch status {
	case smbUnavailable:
		return errors.New("Kernel CIFS support is unavailable; update NanoKVM OS")
	case smbMountFailed:
		return errors.New("Could not connect or authenticate to the signed SMB2/3 share")
	case smbOpenFailed:
		return errors.New("Could not open a regular SMB ISO inside the share")
	case smbChanged:
		return errSMBChanged
	default:
		return errors.New("SMB ISO read failed or returned an incomplete block")
	}
}

// newSMBShared makes the memory of the two window buffers, which the reader
// maps from the returned memfd and fills in place of sending the payload. The
// pages are allocated here and the seals keep the size, so neither process can
// fault on its mapping.
//
// Each reader has its own memory and no later one maps it. A read is given up
// only by closing the reader, after which this source fetches nothing more and
// serves nothing from the buffer that read was filling; a reader that is still
// inside the kernel then writes memory nobody reads.
func newSMBShared() (*os.File, []byte, error) {
	fd, err := unix.MemfdCreate("smb-window", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, nil, err
	}
	memory := os.NewFile(uintptr(fd), "smb-window")
	if err = unix.Fallocate(fd, 0, 0, 2*MaxRead); err == nil {
		_, err = unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_SEAL)
	}
	var shared []byte
	if err == nil {
		shared, err = unix.Mmap(fd, 0, 2*MaxRead, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	}
	if err != nil {
		memory.Close()
		return nil, nil, err
	}
	return memory, shared, nil
}

// A read fills part of one window buffer and never crosses into the other.
func smbSharedRange(shared []byte, at, length uint32) bool {
	return length != 0 && length <= MaxRead && at%MaxRead+length <= MaxRead && uint64(at)+uint64(length) <= uint64(len(shared))
}

// smbSharedOffset finds a fetch target of the window in the shared memory.
func smbSharedOffset(shared, target []byte) (uint32, bool) {
	at := uintptr(unsafe.Pointer(unsafe.SliceData(target))) - uintptr(unsafe.Pointer(unsafe.SliceData(shared)))
	if at >= uintptr(len(shared)) || len(target) > MaxRead || !smbSharedRange(shared, uint32(at), uint32(len(target))) {
		return 0, false
	}
	return uint32(at), true
}

// OpenSMB invokes this same binary in reader mode. Credentials travel only over
// an inherited private socket, then as separate fsconfig values: no command-line
// arguments, credentials file, shell quoting or comma-separated mount options.
func OpenSMB(ctx context.Context, o SMBOptions) (*SMBSource, error) {
	address, _, name, err := parseSMBOptions(o)
	if err != nil {
		return nil, err
	}
	opening, finish := context.WithTimeout(ctx, smbTimeout)
	defer finish()
	select {
	case smbReaderSlot <- struct{}{}:
	case <-opening.Done():
		return nil, smbError(opening, "The previous SMB reader is still shutting down; try again")
	}
	started := false
	defer func() {
		if !started {
			<-smbReaderSlot
		}
	}()
	relay, err := newSMBRelay(opening, address)
	if err != nil {
		return nil, smbError(opening, "SMB connection failed or timed out")
	}
	transferred := false
	defer func() {
		if !transferred {
			relay.Close()
		}
	}()
	memory, shared, err := newSMBShared()
	if err != nil {
		return nil, errors.New("Could not start the SMB reader")
	}
	defer memory.Close()
	defer func() {
		if !transferred {
			_ = unix.Munmap(shared) // no window yet: nothing is lent
		}
	}()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("Could not start the SMB reader")
	}
	local := os.NewFile(uintptr(fds[0]), "smb-parent")
	peer := os.NewFile(uintptr(fds[1]), "smb-child")
	defer peer.Close()
	conn, err := net.FileConn(local)
	local.Close()
	if err != nil {
		return nil, errors.New("Could not start the SMB reader")
	}
	exe, err := os.Executable()
	if err != nil {
		conn.Close()
		return nil, errors.New("Could not locate the SMB reader")
	}
	cmd := exec.Command(exe, smbHelperArg)
	cmd.ExtraFiles = []*os.File{peer, memory}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err = cmd.Start(); err != nil {
		conn.Close()
		return nil, errors.New("Could not start the SMB reader")
	}
	started = true
	peer.Close()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); <-smbReaderSlot; close(done) }()
	var closeOnce sync.Once
	closeReader := func() {
		closeOnce.Do(func() {
			// EOF lets an idle helper close its CIFS file and mount while the
			// server is still reachable. A blocked syscall gets a bounded grace
			// period, then closing the TCP relay breaks its kernel requests.
			conn.Close()
			select {
			case <-done:
				relay.Close()
				return
			case <-time.After(100 * time.Millisecond):
			}
			relay.Close()
			_ = cmd.Process.Kill()
		})
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}
	}
	stopOpening := context.AfterFunc(opening, closeReader)
	ok := false
	defer func() {
		stopOpening()
		if !ok {
			closeReader()
		}
	}()
	deadline, _ := opening.Deadline()
	_ = conn.SetDeadline(deadline)
	request, err := json.Marshal(smbMountOptions{SMBOptions: o, Address: relay.listener.Addr().String()})
	if err != nil || len(request) > 8192 {
		return nil, errors.New("Invalid SMB image options")
	}
	var header [12]byte
	binary.BigEndian.PutUint32(header[:4], uint32(len(request)))
	_, err = conn.Write(header[:4])
	if err == nil {
		_, err = conn.Write(request)
	}
	clear(request)
	if err == nil {
		_, err = io.ReadFull(conn, header[:])
	}
	if err != nil {
		return nil, smbError(opening, "SMB connection failed or timed out")
	}
	if status := binary.BigEndian.Uint32(header[:4]); status != smbOK {
		return nil, smbStatusError(status)
	}
	size := binary.BigEndian.Uint64(header[4:])
	if err = ValidateImage(size); err != nil {
		return nil, err
	}
	// Stop the opening timer callback before handing ownership to the source.
	if !stopOpening() || opening.Err() != nil {
		return nil, smbError(opening, "SMB connection timed out")
	}
	_ = conn.SetDeadline(time.Time{})
	lifetime, cancel := context.WithCancel(ctx)
	s := &SMBSource{Name: path.Base(name), Size: size, ctx: lifetime, cancel: cancel,
		close: closeReader}
	s.stop = context.AfterFunc(lifetime, closeReader)
	// The reader answers one request at a time and cannot cancel one, so a
	// read after a seek first waits for the prefetch. Counting the deadline
	// from the start of that NBD read keeps both below the NBD timeout.
	//
	// The reader fills p, a part of the shared memory, before it answers. Any
	// request left without an answer closes the reader for good.
	var read [16]byte
	fetch := func(_ context.Context, started time.Time, offset uint64, p []byte) error {
		at, inside := smbSharedOffset(shared, p)
		if !inside {
			return errors.New("SMB read buffer is outside the reader memory")
		}
		_ = conn.SetDeadline(started.Add(smbTimeout))
		defer conn.SetDeadline(time.Time{})
		binary.BigEndian.PutUint64(read[:8], offset)
		binary.BigEndian.PutUint32(read[8:12], uint32(len(p)))
		binary.BigEndian.PutUint32(read[12:], at)
		_, err := conn.Write(read[:])
		if err == nil {
			_, err = io.ReadFull(conn, read[:4])
		}
		if err != nil {
			closeReader()
			return smbError(lifetime, "SMB ISO read failed or timed out")
		}
		if status := binary.BigEndian.Uint32(read[:4]); status != smbOK {
			return smbStatusError(status)
		}
		return nil
	}
	s.window = newWindowOver(lifetime, fetch, [2][]byte{shared[:MaxRead:MaxRead], shared[MaxRead:]})
	s.window.size = size
	// The NBD writer may still hold a lent slice when Close returns, and it
	// reaches the window through its read callback. The memory is therefore
	// unmapped only when the window is unreachable.
	runtime.AddCleanup(s.window, func(shared []byte) { _ = unix.Munmap(shared) }, shared)
	ok, transferred = true, true
	return s, nil
}

// This descriptor is never attached to the host's mount namespace. Openat2
// confines the ISO to this read-only share and rejects all symlink/reparse
// traversal; closing the descriptors releases the mount and its connection.
type smbMountOptions struct {
	SMBOptions
	Address string
}

const (
	cifsCCMOnly    = "/sys/module/cifs/parameters/ccm_only"
	cifsCCMRefused = "/sys/module/cifs/parameters/ccm_refused"
)

// The kernel offers AES-128-CCM alone for SMB 3.1.1 encryption: the CryptoDMA
// engine runs all of CCM, and a server picks GCM wherever it is offered. A
// server with CCM disabled takes no cipher, so a share that requires
// encryption refuses the mount; the kernel notes that in ccm_refused, and
// only then is the mount tried once more with the upstream offer. A wrong
// password is not such a case and is not tried twice.
func mountSMBFile(o smbMountOptions) (*os.File, error) {
	_ = os.WriteFile(cifsCCMRefused, []byte("N"), 0)
	file, err := mountSMBOnce(o)
	if err == nil {
		return file, nil
	}
	if refused, _ := os.ReadFile(cifsCCMRefused); len(refused) == 0 || refused[0] != 'Y' {
		return nil, err
	}
	if os.WriteFile(cifsCCMOnly, []byte("N"), 0) != nil {
		return nil, err
	}
	defer os.WriteFile(cifsCCMOnly, []byte("Y"), 0)
	return mountSMBOnce(o)
}

func mountSMBOnce(o smbMountOptions) (*os.File, error) {
	_, share, name, err := parseSMBOptions(o.SMBOptions)
	if err != nil {
		return nil, smbStatusError(smbOpenFailed)
	}
	ip, port, err := net.SplitHostPort(o.Address)
	if err != nil || ip != "127.0.0.1" {
		return nil, smbStatusError(smbMountFailed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), smbTimeout)
	defer cancel()
	fs, err := unix.Fsopen("cifs", unix.FSOPEN_CLOEXEC)
	if errors.Is(err, unix.ENODEV) || errors.Is(err, unix.ENOENT) {
		load, stop := context.WithTimeout(ctx, 5*time.Second)
		err = exec.CommandContext(load, "modprobe", "cifs").Run()
		stop()
		if err == nil {
			fs, err = unix.Fsopen("cifs", unix.FSOPEN_CLOEXEC)
		}
	}
	if err != nil {
		return nil, smbStatusError(smbUnavailable)
	}
	defer unix.Close(fs)
	options := [][2]string{
		{"source", "//" + ip + "/" + share}, {"ip", ip}, {"port", port},
		{"username", o.Username}, {"password", o.Password}, {"domain", o.Domain},
		{"vers", "default"}, {"sec", "ntlmsspi"}, {"cache", "none"},
		{"actimeo", "0"}, {"rsize", strconv.Itoa(MaxRead)}, {"echo_interval", "5"}, {"iocharset", "utf8"},
	}
	for _, option := range options {
		// fsparam_string rejects an empty value. Omission means an empty
		// password/default domain and cannot turn the required user into guest.
		if option[1] == "" {
			continue
		}
		if err = unix.FsconfigSetString(fs, option[0], option[1]); err != nil {
			return nil, smbStatusError(smbMountFailed)
		}
	}
	for _, flag := range []string{"soft", "nosharesock", "nodfs"} {
		if err = unix.FsconfigSetFlag(fs, flag); err != nil {
			return nil, smbStatusError(smbMountFailed)
		}
	}
	if err = unix.FsconfigCreate(fs); err != nil {
		return nil, smbStatusError(smbMountFailed)
	}
	mount, err := unix.Fsmount(fs, unix.FSMOUNT_CLOEXEC, unix.MOUNT_ATTR_RDONLY|unix.MOUNT_ATTR_NOSUID|unix.MOUNT_ATTR_NODEV|unix.MOUNT_ATTR_NOEXEC)
	if err != nil {
		return nil, smbStatusError(smbMountFailed)
	}
	defer unix.Close(mount)
	fd, err := unix.Openat2(mount, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS})
	if err != nil {
		return nil, smbStatusError(smbOpenFailed)
	}
	return os.NewFile(uintptr(fd), "smb-iso"), nil
}

// serveSMBHelper answers a read request (file offset, 8 bytes; length, 4;
// offset in shared, 4; big-endian) with a 4-byte status, after it has read the
// file into that part of shared.
func serveSMBHelper(peer io.ReadWriter, shared []byte, mount func(smbMountOptions) (*os.File, error)) error {
	var header [16]byte
	if _, err := io.ReadFull(peer, header[:4]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[:4])
	if length == 0 || length > 8192 {
		return errors.New("invalid SMB reader request")
	}
	request := make([]byte, length)
	if _, err := io.ReadFull(peer, request); err != nil {
		return err
	}
	var o smbMountOptions
	err := json.Unmarshal(request, &o)
	clear(request)
	if err != nil {
		return err
	}
	f, err := mount(o)
	o = smbMountOptions{}
	status := smbOK
	if err != nil {
		status = smbMountFailed
		if err.Error() == smbStatusError(smbUnavailable).Error() {
			status = smbUnavailable
		}
		if err.Error() == smbStatusError(smbOpenFailed).Error() {
			status = smbOpenFailed
		}
	}
	var reader smbReadFile
	if f != nil {
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() < 0 {
			status = smbOpenFailed
		} else {
			reader = smbReadFile{file: f, size: uint64(info.Size()), mtime: info.ModTime()}
		}
	}
	binary.BigEndian.PutUint32(header[:4], status)
	binary.BigEndian.PutUint64(header[4:12], reader.size)
	if _, err = peer.Write(header[:12]); err != nil {
		return err
	}
	if status != smbOK {
		return errors.New("SMB reader could not open image")
	}
	for {
		if _, err = io.ReadFull(peer, header[:]); err != nil {
			return err
		}
		offset := binary.BigEndian.Uint64(header[:8])
		length := binary.BigEndian.Uint32(header[8:12])
		at := binary.BigEndian.Uint32(header[12:])
		if !smbSharedRange(shared, at, length) || offset > reader.size || uint64(length) > reader.size-offset {
			return errors.New("invalid SMB reader bounds")
		}
		status = smbOK
		if err = reader.read(shared[at:at+length], offset); err != nil {
			status = smbReadFailed
			if errors.Is(err, errSMBChanged) {
				status = smbChanged
			}
		}
		binary.BigEndian.PutUint32(header[:4], status)
		if _, err = peer.Write(header[:4]); err != nil {
			return err
		}
		if status != smbOK {
			return errors.New("SMB reader failed")
		}
	}
}
