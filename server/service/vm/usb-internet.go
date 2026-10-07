package vm

import (
	"context"
	"fmt"
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
	var req proto.SetUSBInternetReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid USB internet setting")
		return
	}
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	runtime := usbinternet.DefaultRuntime()
	if runtime.Enabled() != *req.ExpectedEnabled {
		rsp.ErrRsp(c, -5, "USB internet setting changed; refresh and try again")
		return
	}
	if !deviceExists("/usr/sbin/nkos-usb-internet") {
		rsp.ErrRsp(c, -4, "USB internet requires updated Alpine base package")
		return
	}
	apply := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "sh", "/etc/init.d/S30usbnet", "start").Run(); err != nil {
			return fmt.Errorf("apply USB network: %w", err)
		}
		return nil
	}
	if err := saveUSBInternet(runtime.Boot, *req.Enabled, apply); err != nil {
		log.Errorf("USB internet: %v", err)
		rsp.ErrRsp(c, -3, "failed to apply USB internet setting")
		return
	}
	rsp.OkRspWithData(c, runtime.ReadStatus())
}

// An unavailable uplink/NCM is a persisted waiting state. Actual runtime
// failures roll back the flag and reapply local access without a gadget rebind.
func saveUSBInternet(boot string, enabled bool, apply func() error) error {
	path := filepath.Join(boot, "usb.internet")
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
