package hid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/inputcontrol"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	ModeNormal  = "normal"
	ModeHidOnly = "hid-only"
	ModeFlag    = "/sys/kernel/config/usb_gadget/g0/bcdDevice"

	ModeNormalScript  = "/kvmapp/system/init.d/S03usbdev"
	ModeHidOnlyScript = "/kvmapp/system/init.d/S03usbhid"

	USBDevScript = "/etc/init.d/S03usbdev"
)

var modeMap = map[string]string{
	"0x0510": ModeNormal,
	"0x0511": ModeNormal,
	"0x0623": ModeHidOnly,
	"0x0624": ModeHidOnly,
}

func (s *Service) GetHidMode(c *gin.Context) {
	var rsp proto.Response

	mode, err := GetMode()
	if err != nil {
		rsp.ErrRsp(c, -1, "get HID mode failed")
		return
	}

	rsp.OkRspWithData(c, &proto.GetHidModeRsp{
		Mode: mode,
	})
	log.Debugf("get hid mode: %s", mode)
}

func (s *Service) GetKeyboardLedStatus(c *gin.Context) {
	var rsp proto.Response
	status := GetKeyboardLedStatus()
	updatedAt := ""
	if !status.UpdatedAt.IsZero() {
		updatedAt = status.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	rsp.OkRspWithData(c, &proto.GetKeyboardLedStatusRsp{
		KeyboardEnabled: status.KeyboardEnabled,
		NumLock:         status.NumLock,
		CapsLock:        status.CapsLock,
		ScrollLock:      status.ScrollLock,
		Known:           status.Known,
		UpdatedAt:       updatedAt,
	})
}

func (s *Service) SetHidMode(c *gin.Context) {
	var req proto.SetHidModeReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if req.Mode != ModeNormal && req.Mode != ModeHidOnly {
		rsp.ErrRsp(c, -2, "invalid arguments")
		return
	}

	if mode, _ := GetMode(); req.Mode == mode {
		rsp.OkRsp(c)
		return
	}

	h := GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		h.OpenNoLock()
		h.Unlock()
	}()

	if err := InstallModeScript(req.Mode); err != nil {
		rsp.ErrRsp(c, -3, "operation failed")
		return
	}

	rsp.OkRsp(c)

	log.Println("reboot system...")
	time.Sleep(500 * time.Millisecond)
	_ = exec.Command("reboot").Run()
}

// InstallModeScript refreshes the active init script from the current
// application package without rebinding the gadget. Callers can then perform
// one controlled stop/start with the selected composition.
func InstallModeScript(mode string) error {
	srcScript := ModeNormalScript
	if mode == ModeHidOnly {
		srcScript = ModeHidOnlyScript
	} else if mode != ModeNormal {
		return fmt.Errorf("invalid HID mode %q", mode)
	}
	return copyModeFile(srcScript)
}

func (s *Service) ResetHid(c *gin.Context) {
	var rsp proto.Response

	manual := s.newManualSession()
	defer manual.Close()
	reservation, err := manual.Reserve(c.Request.Context(), inputcontrol.ManualRelativeMouse, false, nil)
	if err != nil {
		log.Errorf("failed to acquire manual control for HID reset: %v", err)
		rsp.ErrRsp(c, -1, "HID control is busy")
		return
	}
	err = manual.Execute(ResetUSBPHY)
	reservation.Complete(err == nil)
	if err != nil {
		log.Errorf("failed to reset hid: %v", err)
		rsp.ErrRsp(c, -1, "failed to reset hid")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("reset hid success")
}

func (s *Service) RecoverUSB(c *gin.Context) {
	var rsp proto.Response

	if err := ResetUSBPHY(); err != nil {
		log.Errorf("failed to recover usb: %v", err)
		rsp.ErrRsp(c, -1, "failed to recover usb")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("recover usb success")
}

func ResetUSBPHY() error {
	h := GetHid()
	h.Lock()
	h.CloseNoLock()
	defer h.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := exec.CommandContext(ctx, "sh", USBDevScript, "restart_phy").Run(); err != nil {
		return fmt.Errorf("restart usb phy: %w", err)
	}

	if err := h.OpenNoLockWithRetry(hidReopenTimeout, hidReopenRetryDelay); err != nil {
		return fmt.Errorf("reopen HID devices after usb phy reset: %w", err)
	}

	return nil
}

func copyModeFile(srcScript string) error {
	// open the source file
	srcFile, err := os.Open(srcScript)
	if err != nil {
		log.Errorf("failed to open %s: %s", srcScript, err)
		return err
	}
	defer func() {
		_ = srcFile.Close()
	}()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		log.Errorf("failed to get %s info: %s", srcScript, err)
		return err
	}

	// create and copy to temporary file
	tmpFile, err := os.CreateTemp(filepath.Dir(USBDevScript), ".S03usbdev-")
	if err != nil {
		log.Errorf("failed to create temp %s: %s", USBDevScript, err)
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	log.Debugf("create temporary file: %s", tmpPath)

	if err := tmpFile.Chmod(srcInfo.Mode()); err != nil {
		_ = tmpFile.Close()
		log.Errorf("failed to set %s mode: %s", tmpPath, err)
		return err
	}

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		_ = tmpFile.Close()
		log.Errorf("failed to copy %s: %s", srcScript, err)
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		log.Errorf("failed to sync %s: %s", tmpPath, err)
		return err
	}

	if err := tmpFile.Close(); err != nil {
		log.Errorf("failed to close %s: %s", tmpPath, err)
		return err
	}

	// replace the target file with the temporary file
	if err := os.Rename(tmpPath, USBDevScript); err != nil {
		log.Errorf("failed to rename %s: %s", tmpPath, err)
		return err
	}

	log.Debugf("copy %s to %s successful", srcScript, USBDevScript)
	return nil
}

func GetMode() (string, error) {
	return getMode(filepath.Dir(ModeFlag))
}

func getMode(gadgetPath string) (string, error) {
	flagPath := filepath.Join(gadgetPath, "bcdDevice")
	data, err := os.ReadFile(flagPath)
	if err != nil {
		log.Errorf("failed to read %s: %s", flagPath, err)
		return "", err
	}

	key := strings.TrimSpace(string(data))
	mode, ok := modeMap[key]
	if !ok {
		log.Errorf("invalid mode flag: %s", key)
		return "", errors.New("invalid mode flag")
	}

	if mode == ModeHidOnly {
		// bcdDevice can survive a partial profile change. Configured network
		// or storage functions take precedence so the endpoint budget cannot
		// hide them and a serial toggle cannot install the wrong init script.
		entries, err := os.ReadDir(filepath.Join(gadgetPath, "configs", "c.1"))
		if err != nil {
			return "", fmt.Errorf("read USB configuration: %w", err)
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Readlink(filepath.Join(gadgetPath, "configs", "c.1", entry.Name()))
			if err != nil {
				return "", fmt.Errorf("read USB function: %w", err)
			}
			function := filepath.Base(target)
			if strings.HasPrefix(function, "rndis.") || strings.HasPrefix(function, "ncm.") || strings.HasPrefix(function, "mass_storage.") || strings.HasPrefix(function, "uac1.") {
				return ModeNormal, nil
			}
		}
	}

	return mode, nil
}
