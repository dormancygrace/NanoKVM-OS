package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/storageidentity"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/remotemedia"
	"NanoKVM-Server/service/hid"
)

var imageDirectory = "/data"

var errInvalidImage = errors.New("invalid image")

var (
	usbGadgetUDC      = "/sys/kernel/config/usb_gadget/g0/UDC"
	usbStorageLink    = "/sys/kernel/config/usb_gadget/g0/configs/c.1/mass_storage.disk0"
	cdromFlag         = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/cdrom"
	mountDevice       = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/file"
	forcedEjectDevice = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/forced_eject"
	inquiryString     = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/inquiry_string"
	roFlag            = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.0/ro"
)

func (s *Service) GetImages(c *gin.Context) {
	var rsp proto.Response

	images, err := listImages()
	if err != nil {
		rsp.ErrRsp(c, -2, "get images failed")
		return
	}

	rsp.OkRspWithData(c, &proto.GetImagesRsp{
		Files: images,
	})
	log.Debugf("get images success, total %d", len(images))
}

// listImages offers exactly the files that imageFile lets MountImage and
// DeleteImage use, so the page never lists an entry it cannot act on.
func listImages() ([]string, error) {
	var images []string
	err := filepath.Walk(imageDirectory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// hidden directories hold the device's own images (the user disk)
		if info.IsDir() && path != imageDirectory && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			if _, err := imageFile(path); err == nil {
				images = append(images, path)
			}
		}
		return nil
	})
	return images, err
}

func (s *Service) MountImage(c *gin.Context) {
	var req proto.MountImageReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	file, err := s.mountImage(req.File, req.Cdrom, req.Force)
	if err != nil {
		var mountErr *imageMountError
		switch {
		case errors.As(err, &mountErr) && mountErr.conflict:
			c.JSON(409, gin.H{"code": mountErr.code, "msg": mountErr.msg})
		case errors.As(err, &mountErr):
			rsp.ErrRsp(c, mountErr.code, mountErr.msg)
		default:
			rsp.ErrRsp(c, -2, "mount image failed")
		}
		return
	}

	rsp.OkRsp(c)
	log.Debugf("mount image %s success", file)
}

// Images lists the images in /data that MountImageFile accepts.
func (s *Service) Images() ([]string, error) { return listImages() }

// MountImageFile puts an image of /data on the virtual disk, or ejects the
// medium when name is empty: the checks and the effect of the web page's
// mount (POST /api/storage/image/mount), for other services such as MCP.
func (s *Service) MountImageFile(name string, cdrom bool) error {
	_, err := s.mountImage(name, cdrom, false)
	return err
}

// mountImage validates name, refuses while remote media or network boot own
// the virtual disk, and mounts; it returns the validated path.
func (s *Service) mountImage(name string, cdrom, force bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remote != nil {
		return "", &imageMountError{code: -2, msg: "Disconnect remote media before mounting another image", conflict: true}
	}
	if s.netboot != nil {
		return "", &imageMountError{code: -2, msg: "Turn off network boot before mounting another image", conflict: true}
	}
	if force && name != "" {
		return "", &imageMountError{code: -1, msg: "force is only valid when ejecting an image"}
	}
	// Use only the validated path below, never the name given.
	file := ""
	if name != "" {
		var err error
		if file, err = imageFile(name); err != nil {
			return "", &imageMountError{code: -2, msg: "invalid image"}
		}
	}
	return file, mountLocalImage(file, cdrom, force)
}

// imageMountError carries the API code and message of a failed mount;
// conflict: another medium owns the virtual disk (HTTP 409).
type imageMountError struct {
	code     int
	msg      string
	conflict bool
}

func (e *imageMountError) Error() string { return e.msg }

// mountLocalImage puts an already validated file (or no file: eject) on the
// virtual disk. The drive is removable, so the computer learns of a medium
// put into the empty drive or taken out from the drive itself, as with a card
// reader or a CD drive. The USB devices reconnect only when the drive changes
// (setDrive) or when the image replaces a mounted one: that swap takes no
// time, the computer never sees the drive empty, and Windows went on showing
// the old volume's files for seconds. Callers hold s.mu.
func mountLocalImage(file string, cdrom, force bool) error {
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	if err := requireActiveUSBStorage(); err != nil {
		log.Warnf("USB storage is unavailable: %s", err)
		return &imageMountError{code: -3, msg: "Enable USB storage before mounting an image"}
	}

	if file != "" && cdrom {
		info, statErr := os.Stat(file)
		if statErr != nil || !info.Mode().IsRegular() {
			return &imageMountError{code: -2, msg: "Cannot read the selected optical image"}
		}
		_, dvdErr := os.Stat(filepath.Join(filepath.Dir(mountDevice), "dvd"))
		if err := remotemedia.ValidateOptical(uint64(info.Size()), dvdErr == nil); err != nil {
			return &imageMountError{code: -2, msg: err.Error()}
		}
	}

	// unmount
	previous, _ := os.ReadFile(mountDevice)
	replaced := strings.TrimSpace(string(previous)) != ""
	if err := ejectLocalImage(force, os.WriteFile); err != nil {
		log.Errorf("unmount file failed: %s", err)
		return &imageMountError{code: -4, msg: "unmount image failed"}
	}
	// Without a file the device stays unmounted (no media), and the drive
	// stays what it was: an empty disk or CD drive.
	// Previously this wrote /dev/mmcblk0p3, exposing the NanoKVM's raw
	// eMMC partition as a USB disk — causing Legacy BIOS boot hangs.
	// See: https://github.com/sipeed/NanoKVM/issues/633
	if file == "" {
		return nil
	}

	// A CD is read-only, a disk image is written by the computer. Always
	// apply the selected mode, including after a remote DVD disconnect.
	kind := "disk"
	if cdrom {
		kind = "cdrom"
	}
	changed, err := setDrive(cdrom, storageidentity.Read(hid.USBFlagsDir).Inquiry(kind))
	if err != nil {
		log.Errorf("set the drive to %s failed: %s", kind, err)
		return &imageMountError{code: -2, msg: "set drive mode failed"}
	}
	if err := os.WriteFile(mountDevice, []byte(file), 0o666); err != nil {
		log.Errorf("mount file %s failed: %s", file, err)
		return &imageMountError{code: -2, msg: "mount image failed"}
	}
	if !changed && !replaced {
		return nil
	}
	if err := reconnectUSB(); err != nil {
		log.Errorf("reconnect USB failed: %s", err)
		return &imageMountError{code: -2, msg: "reconnect USB failed"}
	}
	return nil
}

// setDrive makes the empty virtual drive a CD drive (read-only) or a disk
// (writable) with the given SCSI name, and reports whether that changed
// anything. The computer reads a drive's type and name once, when the USB
// devices connect, so a change needs a reconnection (reconnectUSB) and
// nothing else does: with the same drive, a mount is a medium put in.
// Callers hold the HID lock.
func setDrive(cdrom bool, inquiry string) (changed bool, err error) {
	flag := "0"
	if cdrom {
		flag = "1"
	}
	before := driveIdentity()
	for _, item := range []struct{ path, value string }{{roFlag, flag}, {cdromFlag, flag}, {inquiryString, inquiry}} {
		if err := os.WriteFile(item.path, []byte(item.value), 0o666); err != nil {
			return false, fmt.Errorf("%s: %w", filepath.Base(item.path), err)
		}
	}
	return driveIdentity() != before, nil
}

// driveIdentity is the drive's type and name as the kernel holds them.
func driveIdentity() string {
	flag, _ := os.ReadFile(cdromFlag)
	name, _ := os.ReadFile(inquiryString)
	return strings.TrimSpace(string(flag)) + "\n" + strings.TrimRight(string(name), "\n")
}

func requireActiveUSBStorage() error {
	udc, err := os.ReadFile(usbGadgetUDC)
	if err != nil {
		return fmt.Errorf("read USB controller binding: %w", err)
	}
	if strings.TrimSpace(string(udc)) == "" {
		return fmt.Errorf("USB gadget is not bound")
	}
	info, err := os.Lstat(usbStorageLink)
	if err != nil {
		return fmt.Errorf("read USB storage link: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("USB storage function is not linked")
	}
	for _, path := range []string{mountDevice, forcedEjectDevice, roFlag, cdromFlag, inquiryString} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("read USB storage attribute %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func ejectLocalImage(force bool, writeFile func(string, []byte, os.FileMode) error) error {
	err := writeFile(mountDevice, []byte("\n"), 0o666)
	if err == nil || !force {
		return err
	}
	if forceErr := writeFile(forcedEjectDevice, []byte("1\n"), 0o666); forceErr != nil {
		return fmt.Errorf("normal eject failed: %v; forced eject failed: %w", err, forceErr)
	}
	return nil
}

func (s *Service) GetMountedImage(c *gin.Context) {
	var rsp proto.Response

	image, err := mountedImage()
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	rsp.OkRspWithData(c, &proto.GetMountedImageRsp{
		File: image,
	})
}

// ErrNoUSBStorage: the USB gadget has no mass storage function bound, so
// there is no virtual disk to show or mount.
var ErrNoUSBStorage = errors.New("USB storage is off: enable it in Settings > USB Gadget")

// MountedImage is the file on the virtual disk ("" when none) and whether it
// is shown as an optical drive; ErrNoUSBStorage without a virtual disk.
func (s *Service) MountedImage() (string, bool, error) {
	if err := requireActiveUSBStorage(); err != nil {
		return "", false, ErrNoUSBStorage
	}
	image, err := mountedImage()
	if err != nil || image == "" {
		return image, false, err
	}
	flag, err := os.ReadFile(cdromFlag)
	return image, strings.TrimSpace(string(flag)) == "1", err
}

func mountedImage() (string, error) {
	mode, err := hid.GetMode()
	if err != nil {
		return "", errors.New("get HID mode failed")
	}
	if mode == hid.ModeHidOnly {
		return "", nil
	}

	content, err := os.ReadFile(mountDevice)
	if err != nil {
		return "", errors.New("read failed")
	}

	image := strings.TrimSpace(string(content))
	if image == "/dev/mmcblk0p3" {
		// Backward compat: treat eMMC partition as "no image mounted"
		image = ""
	}
	return image, nil
}

func (s *Service) GetCdRom(c *gin.Context) {
	var rsp proto.Response

	content, err := os.ReadFile(cdromFlag)
	if err != nil {
		rsp.ErrRsp(c, -1, "read failed")
		return
	}

	flag := strings.ReplaceAll(string(content), "\n", "")
	flatInt, err := strconv.ParseInt(flag, 10, 64)
	if err != nil {
		rsp.ErrRsp(c, -2, "parse failed")
		return
	}

	data := &proto.GetCdRomRsp{
		Cdrom: flatInt,
	}

	rsp.OkRspWithData(c, data)
}

func (s *Service) DeleteImage(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var req proto.DeleteImageReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	file, err := imageFile(req.File)
	if err != nil {
		rsp.ErrRsp(c, -2, "invalid arguments")
		return
	}

	if err := RemoveImage(file); err != nil {
		rsp.ErrRsp(c, -3, err.Error())
		log.Errorf("failed to remove file %s: %s", file, err)
		return
	}

	rsp.OkRsp(c)
	log.Debugf("delete image %s success", file)
}

// imageFile accepts only what GetImages lists: a regular .iso or .img file
// below imageDirectory, reached without symbolic links. A prefix check alone
// let "/data/../" names reach any file, and mounting exposed it to the host.
func imageFile(name string) (string, error) {
	path := filepath.Clean(name)
	lower := strings.ToLower(path)
	if !strings.HasPrefix(path, imageDirectory+string(filepath.Separator)) ||
		!(strings.HasSuffix(lower, ".iso") || strings.HasSuffix(lower, ".img")) {
		return "", errInvalidImage
	}
	// the hidden directories are the device's own (the user disk)
	for _, part := range strings.Split(strings.TrimPrefix(path, imageDirectory), string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return "", errInvalidImage
		}
	}
	if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path {
		return "", errInvalidImage
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return "", errInvalidImage
	}
	return path, nil
}
