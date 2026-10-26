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
	"strconv"
	"sync"
	"syscall"
	"time"

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
		if peer == nil {
			os.Exit(1)
		}
		err := serveSMBHelper(peer, mountSMBFile)
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
	cmd.ExtraFiles = []*os.File{peer}
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
		close: closeReader, buffer: make([]byte, MaxRead), next: ^uint64(0)}
	s.stop = context.AfterFunc(lifetime, closeReader)
	s.read = func(p []byte, offset uint64) error {
		_ = conn.SetDeadline(time.Now().Add(smbTimeout))
		defer conn.SetDeadline(time.Time{})
		binary.BigEndian.PutUint64(header[:8], offset)
		binary.BigEndian.PutUint32(header[8:], uint32(len(p)))
		_, err := conn.Write(header[:])
		if err == nil {
			_, err = io.ReadFull(conn, header[:4])
		}
		if err == nil {
			if status := binary.BigEndian.Uint32(header[:4]); status != smbOK {
				return smbStatusError(status)
			}
			_, err = io.ReadFull(conn, p)
		}
		if err != nil {
			closeReader()
			return smbError(lifetime, "SMB ISO read failed or timed out")
		}
		return nil
	}
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

func mountSMBFile(o smbMountOptions) (*os.File, error) {
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

func serveSMBHelper(peer io.ReadWriter, mount func(smbMountOptions) (*os.File, error)) error {
	var header [12]byte
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
	binary.BigEndian.PutUint64(header[4:], reader.size)
	if _, err = peer.Write(header[:]); err != nil {
		return err
	}
	if status != smbOK {
		return errors.New("SMB reader could not open image")
	}
	buffer := make([]byte, MaxRead)
	for {
		if _, err = io.ReadFull(peer, header[:]); err != nil {
			return err
		}
		offset := binary.BigEndian.Uint64(header[:8])
		length := binary.BigEndian.Uint32(header[8:])
		if length == 0 || length > MaxRead || offset > reader.size || uint64(length) > reader.size-offset {
			return errors.New("invalid SMB reader bounds")
		}
		status = smbOK
		if err = reader.read(buffer[:length], offset); err != nil {
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
		if _, err = peer.Write(buffer[:length]); err != nil {
			return err
		}
	}
}
