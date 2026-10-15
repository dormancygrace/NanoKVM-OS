package storage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
)

// The user disk: a writable FAT32 image on the data partition that the
// connected computer sees as a third drive of the virtual storage device
// (lun.2), for BIOS update files, profiles and the like. The image lives in
// a hidden directory, so the Images menu never offers it for lun.0. Whether
// it is attached is part of the USB composition (usb.userdisk marker,
// S03usbdev); this file creates, describes and removes the image.

var (
	userDiskDir   = "/data/.nanokvm"
	userDiskImage = "/data/.nanokvm/user-disk.img"
	userDiskLun   = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.2/file"
	userDiskFlag  = "/boot/usb.userdisk"
	// BusyBox mkfs.vfat on the device makes FAT32 whatever the size and
	// accepts -F and -s without minding them; dosfstools (the build hosts)
	// needs them to make FAT32 with enough clusters at the small sizes.
	userDiskFormat = func(path string, sizeMiB int) error {
		output, err := exec.Command("mkfs.vfat", "-F", "32", "-s", fmt.Sprint(userDiskSectorsPerCluster(sizeMiB)),
			"-n", userDiskLabel, path).CombinedOutput()
		if err != nil {
			return fmt.Errorf("mkfs.vfat: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
)

// userDiskMu covers every change to the image and every composition change
// that attaches or detaches it, including mounted file transfers. A format renaming
// a new image over the one a concurrent apply has just attached would leave
// the host writing to an unlinked inode. Create and delete hold it while
// they check the state and replace the file; the composition takes it with
// TryLock (it runs under the HID lock, which must not wait for a format of
// several GiB) and refuses while the image is being accessed. Create and
// delete take it with TryLock as well: a file transfer holds it for as long
// as the client keeps it moving, and they hold s.mu, which the image and
// remote media handlers need.
var userDiskMu sync.Mutex

// ErrUserDiskBusy: another operation owns the image, or a private mount remains.
var ErrUserDiskBusy = errors.New("the user disk is busy; wait for the current operation to finish")

// LockUserDiskForComposition is taken by a composition change that attaches
// or detaches the image, for the whole apply. It does not wait.
func LockUserDiskForComposition() (unlock func(), err error) {
	if !userDiskMu.TryLock() {
		return nil, ErrUserDiskBusy
	}
	if err := userDiskUnmounted(); err != nil {
		userDiskMu.Unlock()
		return nil, err
	}
	return userDiskMu.Unlock, nil
}

// userDiskSectorsPerCluster keeps the cluster count at or above FAT32's
// 65525 (one 512-byte sector per cluster up to 256 MiB) without making a
// large image slow (4 KiB clusters from 512 MiB on, as mkfs.vfat picks).
func userDiskSectorsPerCluster(sizeMiB int) int {
	if sizeMiB <= 256 {
		return 1
	}
	return 8
}

const (
	userDiskLabel = "NKVMDATA"
	// FAT32 wants at least 65525 clusters; 64 MiB of 512-byte clusters is
	// the smallest size mkfs.vfat lays out as a FAT32 Windows accepts.
	userDiskMinMiB = 64
	userDiskMaxMiB = 4096
)

var userDiskSizes = []int{64, 128, 256, 512, 1024, 2048, 4096}

// UserDiskImagePath is where the composition code looks for the image.
func UserDiskImagePath() string { return userDiskImage }

// UserDiskExists says whether an image is there to attach.
func UserDiskExists() bool {
	info, err := os.Stat(userDiskImage)
	return err == nil && info.Mode().IsRegular()
}

func userDiskAttached() bool {
	data, err := os.ReadFile(userDiskLun)
	return err == nil && strings.TrimSpace(string(data)) == userDiskImage
}

func userDiskEnabled() bool {
	data, err := os.ReadFile(userDiskFlag)
	return err == nil && strings.TrimSpace(string(data)) == userDiskImage
}

func userDiskState() proto.UserDiskRsp {
	rsp := proto.UserDiskRsp{File: userDiskImage, Label: userDiskLabel, Sizes: userDiskSizes,
		Enabled: userDiskEnabled(), Attached: userDiskAttached()}
	if info, err := os.Stat(userDiskImage); err == nil && info.Mode().IsRegular() {
		rsp.Exists = true
		rsp.SizeMiB = int(info.Size() / (1024 * 1024))
		rsp.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	}
	return rsp
}

func (s *Service) GetUserDisk(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, userDiskState())
}

// createUserDisk writes a fresh FAT32 image of sizeMiB in place of any
// existing one. The image is built under a temporary name and renamed, so
// a failure leaves the old image as it was.
func createUserDisk(sizeMiB int) error {
	if sizeMiB < userDiskMinMiB || sizeMiB > userDiskMaxMiB {
		return fmt.Errorf("size must be between %d and %d MiB", userDiskMinMiB, userDiskMaxMiB)
	}
	if err := os.MkdirAll(userDiskDir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(userDiskDir, ".user-disk-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Truncate(int64(sizeMiB) * 1024 * 1024); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := userDiskFormat(tmpPath, sizeMiB); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, userDiskImage)
}

// CreateUserDisk makes (or remakes) the image. Refused while the image is
// attached: replacing a file under a live LUN leaves the host with a stale
// view, and the kernel keeps the old inode.
func (s *Service) CreateUserDisk(c *gin.Context) {
	var req proto.CreateUserDiskReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !userDiskMu.TryLock() {
		rsp.ErrRsp(c, -6, ErrUserDiskBusy.Error())
		return
	}
	defer userDiskMu.Unlock()
	if err := userDiskUnmounted(); err != nil {
		rsp.ErrRsp(c, -6, "the user disk is mounted locally")
		return
	}
	if err := userDiskAvailable(); err != nil {
		rsp.ErrRsp(c, -2, "turn the user disk off before creating it anew")
		return
	}
	if err := createUserDisk(req.SizeMiB); err != nil {
		log.Errorf("create user disk: %v", err)
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	rsp.OkRspWithData(c, userDiskState())
}

// DeleteUserDisk removes the image; refused while it is attached or enabled.
func (s *Service) DeleteUserDisk(c *gin.Context) {
	var rsp proto.Response
	s.mu.Lock()
	defer s.mu.Unlock()
	if !userDiskMu.TryLock() {
		rsp.ErrRsp(c, -6, ErrUserDiskBusy.Error())
		return
	}
	defer userDiskMu.Unlock()
	if err := userDiskUnmounted(); err != nil {
		rsp.ErrRsp(c, -6, "the user disk is mounted locally")
		return
	}
	if err := userDiskAvailable(); err != nil {
		rsp.ErrRsp(c, -2, "turn the user disk off before deleting it")
		return
	}
	if err := os.Remove(userDiskImage); err != nil && !errors.Is(err, os.ErrNotExist) {
		rsp.ErrRsp(c, -3, "remove failed")
		return
	}
	rsp.OkRspWithData(c, userDiskState())
}
