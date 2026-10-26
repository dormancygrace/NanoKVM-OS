package storage

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"NanoKVM-Server/internal/storageidentity"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/remotemedia"
	"NanoKVM-Server/service/hid"
	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

type remoteBlockDevice interface {
	Close()
	Stopped() <-chan struct{}
}

var openRemoteBlockDevice = func(size uint64, read remotemedia.ReadBlock) (remoteBlockDevice, error) {
	return remotemedia.Open(size, read)
}

// The browser adapter retains #486's buffer ownership, aligned allocations and
// two-slot adaptive prefetch. Other sources need not use a WebSocket transport.
func (r *remoteReader) ReadBlock(offset uint64, length uint32) ([]byte, error) {
	return r.read(offset, length)
}
func (r *remoteReader) Close() { r.close() }

func (s *Service) reserveRemote(r *remoteSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remote != nil {
		return errors.New("Remote media is already connected")
	}
	if s.netboot != nil {
		return errors.New("Turn off network boot before connecting an ISO")
	}
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	if err := requireActiveUSBStorage(); err != nil {
		return errors.New("Enable USB storage before connecting an ISO")
	}
	current, err := os.ReadFile(mountDevice)
	if err != nil {
		return errors.New("Cannot read USB storage state")
	}
	if strings.TrimSpace(string(current)) != "" {
		return errors.New("Unmount the current image first")
	}
	s.remote, s.remoteError, s.remoteKind = r, "", r.kind
	return nil
}

func (s *Service) releaseRemote(r *remoteSession, err error) {
	r.close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remote == r {
		s.remote = nil
		if err != nil && !errors.Is(err, context.Canceled) {
			s.remoteError = err.Error()
		}
	}
}

// serveRemote owns the common NBD/gadget lifetime. r.close cancels the source
// before the kernel attachment is closed, so a blocked network read cannot
// hold USB teardown. Source buffers stay alive until NBD has stopped.
func (s *Service) serveRemote(r *remoteSession, source remotemedia.Source, mounted func() error) (result error) {
	var device remoteBlockDevice
	defer func() {
		if result != nil && !errors.Is(result, context.Canceled) && r.ws != nil {
			_ = r.send(gin.H{"type": "error", "message": result.Error()})
		}
		r.close()
		if device != nil {
			device.Close()
		}
		source.Close()
		if device == nil {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		h := hid.GetHid()
		h.Lock()
		defer h.Unlock()
		if data, _ := os.ReadFile(mountDevice); strings.TrimSpace(string(data)) == remotemedia.DevicePath {
			if err := os.WriteFile(mountDevice, []byte("\n"), 0600); err != nil {
				_ = os.WriteFile(forcedEjectDevice, []byte("1"), 0600)
			}
		}
		r.mounted = false
	}()
	select {
	case <-r.closed:
		return context.Canceled
	default:
	}
	_, dvdErr := os.Stat(filepath.Join(filepath.Dir(mountDevice), "dvd"))
	if err := remotemedia.ValidateOptical(r.size, dvdErr == nil); err != nil {
		return err
	}
	readError := make(chan error, 1)
	var err error
	device, err = openRemoteBlockDevice(r.size, func(offset uint64, length uint32) ([]byte, error) {
		data, err := source.ReadBlock(offset, length)
		if err == nil && len(data) != int(length) {
			err = errors.New("Remote image returned an incomplete block")
		}
		if err != nil {
			select {
			case readError <- err:
			default:
			}
		}
		if err == nil && r.kind != "browser" {
			r.readBytes.Add(uint64(len(data)))
		}
		return data, err
	})
	if err != nil {
		return err
	}
	select {
	case <-r.closed:
		return context.Canceled
	default:
	}
	s.mu.Lock()
	h := hid.GetHid()
	h.Lock()
	err = requireActiveUSBStorage()
	if err == nil {
		current, readErr := os.ReadFile(mountDevice)
		if readErr != nil || strings.TrimSpace(string(current)) != "" {
			err = errors.New("USB medium changed while connecting")
		}
	}
	if err == nil {
		for _, item := range []struct{ path, value string }{{roFlag, "1"}, {cdromFlag, "1"}, {inquiryString, storageidentity.Read(hid.USBFlagsDir).Inquiry("remote")}, {mountDevice, remotemedia.DevicePath}} {
			if err = os.WriteFile(item.path, []byte(item.value), 0600); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = reconnectRemoteUSB()
	}
	if err == nil {
		r.mounted = true
	}
	h.Unlock()
	s.mu.Unlock()
	if err != nil {
		return errors.New("Could not connect the remote ISO to USB storage")
	}
	if mounted != nil {
		if err := mounted(); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.closed:
			return context.Canceled
		case err := <-readError:
			return err
		case <-device.Stopped():
			// Serve can stop immediately after a failed callback. Keep the
			// source's actionable error when both notifications are ready.
			select {
			case err := <-readError:
				return err
			default:
			}
			return errors.New("Remote media device disconnected")
		case <-ticker.C:
			h.Lock()
			data, readErr := os.ReadFile(mountDevice)
			activeErr := requireActiveUSBStorage()
			h.Unlock()
			if readErr != nil || activeErr != nil || strings.TrimSpace(string(data)) != remotemedia.DevicePath {
				return errors.New("Remote ISO was ejected or USB storage was disabled")
			}
		}
	}
}

// ConnectHTTP reserves the medium before starting network I/O. Its lifetime is
// owned by the device, independent of the requesting browser or HTTP request.
func (s *Service) ConnectHTTP(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req remotemedia.HTTPOptions
	var rsp proto.Response
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "Invalid HTTP image options")
		return
	}
	if err := remotemedia.ValidateHTTPOptions(req); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	s.connectRemoteURL(c, "http", func(ctx context.Context) (remotemedia.Source, string, uint64, error) {
		source, err := remotemedia.OpenHTTP(ctx, req)
		if err != nil {
			return nil, "", 0, err
		}
		return source, source.Name, source.Size, nil
	})
}

func (s *Service) ConnectSMB(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req remotemedia.SMBOptions
	var rsp proto.Response
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "Invalid SMB image options")
		return
	}
	if err := remotemedia.ValidateSMBOptions(req); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	s.connectRemoteURL(c, "smb", func(ctx context.Context) (remotemedia.Source, string, uint64, error) {
		source, err := remotemedia.OpenSMB(ctx, req)
		if err != nil {
			return nil, "", 0, err
		}
		return source, source.Name, source.Size, nil
	})
}

func (s *Service) connectRemoteURL(c *gin.Context, kind string, open func(context.Context) (remotemedia.Source, string, uint64, error)) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &remoteSession{kind: kind, name: "remote.iso", closed: make(chan struct{}), cancel: cancel}
	if err := s.reserveRemote(r); err != nil {
		r.close()
		c.JSON(409, gin.H{"code": -2, "msg": err.Error()})
		return
	}
	go func() {
		source, name, size, err := open(ctx)
		if err == nil {
			s.mu.Lock()
			r.name, r.size = name, size
			s.mu.Unlock()
			err = s.serveRemote(r, source, nil)
		}
		s.releaseRemote(r, err)
	}()
	var rsp proto.Response
	rsp.OkRsp(c)
}

// A crashed server closes its NBD socket, but configfs can still name the dead
// disk. Never detach an NBD device that belongs to a currently running owner.
func (s *Service) CleanupRemote() {
	s.cleanupRemote(remotemedia.DevicePath, "/sys/block/nbd0/pid")
}

func (s *Service) cleanupRemote(devicePath, pidPath string) {
	// Use the same lock as remotemedia.Open, including while examining configfs.
	// The PID check alone races with a new attachment by another server process.
	device, err := os.Open(devicePath)
	if err != nil {
		return
	}
	defer device.Close()
	if err := unix.Flock(int(device.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return
	}
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	pid, err := os.ReadFile(pidPath)
	if (err != nil && !errors.Is(err, os.ErrNotExist)) || strings.TrimSpace(string(pid)) != "" {
		return
	}
	if data, _ := os.ReadFile(mountDevice); strings.TrimSpace(string(data)) == remotemedia.DevicePath {
		if err := os.WriteFile(mountDevice, []byte("\n"), 0600); err != nil {
			_ = os.WriteFile(forcedEjectDevice, []byte("1"), 0600)
		}
	}
}
