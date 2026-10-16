package storage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remote != nil {
		c.JSON(409, gin.H{"code": -2, "msg": "Disconnect browser media before mounting another image"})
		return
	}
	if s.netboot != nil {
		c.JSON(409, gin.H{"code": -2, "msg": "Turn off network boot before mounting another image"})
		return
	}

	var req proto.MountImageReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if req.Force && req.File != "" {
		rsp.ErrRsp(c, -1, "force is only valid when ejecting an image")
		return
	}
	// Use only the validated path below, never req.File.
	file := ""
	if req.File != "" {
		var err error
		if file, err = imageFile(req.File); err != nil {
			rsp.ErrRsp(c, -2, "invalid image")
			return
		}
	}
	if err := mountLocalImage(file, req.Cdrom, req.Force); err != nil {
		var mountErr *imageMountError
		if errors.As(err, &mountErr) {
			rsp.ErrRsp(c, mountErr.code, mountErr.msg)
		} else {
			rsp.ErrRsp(c, -2, "mount image failed")
		}
		return
	}

	rsp.OkRsp(c)
	log.Debugf("mount image %s success", file)
}

// imageMountError carries the API code and message of a failed mount.
type imageMountError struct {
	code int
	msg  string
}

func (e *imageMountError) Error() string { return e.msg }

// mountLocalImage puts an already validated file (or no file: eject) on the
// virtual disk and rebinds the gadget so the host sees the new medium. Callers
// hold s.mu.
func mountLocalImage(file string, cdrom, force bool) error {
	if err := requireActiveUSBStorage(); err != nil {
		log.Warnf("USB storage is unavailable: %s", err)
		return &imageMountError{-3, "Enable USB storage before mounting an image"}
	}

	if file != "" && cdrom {
		info, statErr := os.Stat(file)
		if statErr != nil || !info.Mode().IsRegular() {
			return &imageMountError{-2, "Cannot read the selected optical image"}
		}
		_, dvdErr := os.Stat(filepath.Join(filepath.Dir(mountDevice), "dvd"))
		if err := remotemedia.ValidateOptical(uint64(info.Size()), dvdErr == nil); err != nil {
			return &imageMountError{-2, err.Error()}
		}
	}

	// cdrom and ro flag
	// set to 0 when unmount image
	// set to 1 when mount image and the CD-ROM is enabled
	// Always apply the selected mode, including after a remote DVD disconnect.
	flag := "0"
	if file != "" && cdrom {
		flag = "1"
	}

	// unmount
	if err := ejectLocalImage(force, os.WriteFile); err != nil {
		log.Errorf("unmount file failed: %s", err)
		return &imageMountError{-4, "unmount image failed"}
	}

	// ro flag
	if err := os.WriteFile(roFlag, []byte(flag), 0o666); err != nil {
		log.Errorf("set ro flag failed: %s", err)
		return &imageMountError{-2, "set ro flag failed"}
	}

	// cdrom flag
	if err := os.WriteFile(cdromFlag, []byte(flag), 0o666); err != nil {
		log.Errorf("set cdrom flag failed: %s", err)
		return &imageMountError{-2, "set cdrom flag failed"}
	}

	inquiryVen := "NanoKVM"
	inquiryPrd := "USB Mass Storage"
	inquiryVer := 0x0520
	if cdrom {
		inquiryPrd = "USB CD/DVD-ROM"
	}
	inquiryData := fmt.Sprintf("%-8s%-16s%04x", inquiryVen, inquiryPrd, inquiryVer)

	if err := os.WriteFile(inquiryString, []byte(inquiryData), 0o666); err != nil {
		log.Errorf("set inquiry %s failed: %s", inquiryData, err)
		return &imageMountError{-2, "set inquiry failed"}
	}

	// mount
	if file != "" {
		if err := os.WriteFile(mountDevice, []byte(file), 0o666); err != nil {
			log.Errorf("mount file %s failed: %s", file, err)
			return &imageMountError{-2, "mount image failed"}
		}
	}
	// Without a file the device stays unmounted (no media).
	// Previously this wrote /dev/mmcblk0p3, exposing the NanoKVM's raw
	// eMMC partition as a USB disk — causing Legacy BIOS boot hangs.
	// See: https://github.com/sipeed/NanoKVM/issues/633

	h := hid.GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		h.OpenNoLock()
		h.Unlock()
	}()

	// reset usb
	commands := []string{
		"echo > /sys/kernel/config/usb_gadget/g0/UDC",
		"ls /sys/class/udc/ | cat > /sys/kernel/config/usb_gadget/g0/UDC",
	}

	for _, command := range commands {
		err := exec.Command("sh", "-c", command).Run()
		if err != nil {
			return &imageMountError{-2, "execute command failed"}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
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

	mode, err := hid.GetMode()
	if err != nil {
		rsp.ErrRsp(c, -2, "get HID mode failed")
		return
	}

	if mode == hid.ModeHidOnly {
		rsp.OkRspWithData(c, &proto.GetMountedImageRsp{
			File: "",
		})
		return
	}

	content, err := os.ReadFile(mountDevice)
	if err != nil {
		rsp.ErrRsp(c, -2, "read failed")
		return
	}

	image := strings.TrimSpace(string(content))
	if image == "/dev/mmcblk0p3" {
		// Backward compat: treat eMMC partition as "no image mounted"
		image = ""
	}

	data := &proto.GetMountedImageRsp{
		File: image,
	}

	rsp.OkRspWithData(c, data)
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

	if err := os.Remove(file); err != nil {
		rsp.ErrRsp(c, -3, "remove file failed")
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
