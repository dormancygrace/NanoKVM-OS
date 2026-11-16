package vm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/usbinternet"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func (s *Service) GetUSBInternet(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, usbinternet.DefaultRuntime().ReadStatus())
}

func (s *Service) SetUSBInternet(c *gin.Context) {
	setUSBNetworkFlag(c, "usb.internet", usbinternet.Runtime.Enabled, "USB internet")
}

// SetUSBHostAccess turns the usb0 input filter off (enabled) or on: whether
// the managed computer may open every service of the NanoKVM over USB.
func (s *Service) SetUSBHostAccess(c *gin.Context) {
	setUSBNetworkFlag(c, usbinternet.HostAccessFlag, usbinternet.Runtime.HostAccess, "USB host access")
}

// applyUSBNetwork reconciles USB networking (address, DHCP/DNS, sharing and
// the usb0 input filter) with the flags; it changes nothing while the
// composition has no USB network.
func applyUSBNetwork() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "sh", "/etc/init.d/S30usbnet", "start").Run(); err != nil {
		return fmt.Errorf("apply USB network: %w", err)
	}
	return nil
}

// setUSBNetworkFlag saves one USB network flag ({enabled, expectedEnabled})
// and applies it without a gadget rebind.
func setUSBNetworkFlag(c *gin.Context, name string, current func(usbinternet.Runtime) bool, what string) {
	var req proto.SetUSBInternetReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid "+what+" setting")
		return
	}
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	runtime := usbinternet.DefaultRuntime()
	if current(runtime) != *req.ExpectedEnabled {
		rsp.ErrRsp(c, -5, what+" setting changed; refresh and try again")
		return
	}
	if !deviceExists("/usr/sbin/nkos-usb-internet") {
		rsp.ErrRsp(c, -4, what+" requires updated Alpine base package")
		return
	}
	if err := saveUSBFlag(runtime.Boot, name, *req.Enabled, applyUSBNetwork); err != nil {
		log.Errorf("%s: %v", what, err)
		rsp.ErrRsp(c, -3, "failed to apply "+what+" setting")
		return
	}
	rsp.OkRspWithData(c, runtime.ReadStatus())
}

// An unavailable uplink/NCM is a persisted waiting state. Actual runtime
// failures roll back the flag and reapply local access without a gadget rebind.
func saveUSBFlag(boot, name string, enabled bool, apply func() error) error {
	path := filepath.Join(boot, name)
	if err := os.MkdirAll(boot, 0o755); err != nil {
		return err
	}
	before, err := snapshotUSBFile(path)
	if err != nil {
		return err
	}
	if err = writeUSBFile(path, usbFileSnapshot{exists: enabled, mode: 0644}); err != nil {
		return err
	}
	if err = apply(); err == nil {
		return nil
	}
	if rollback := writeUSBFile(path, before); rollback != nil {
		return fmt.Errorf("%w; restore setting: %v", err, rollback)
	}
	if rollback := apply(); rollback != nil {
		return fmt.Errorf("%w; restore runtime: %v", err, rollback)
	}
	return err
}
