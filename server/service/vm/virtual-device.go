package vm

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"
)

const (
	virtualNetwork = "/boot/usb.rndis0"
	virtualNCM     = "/boot/usb.ncm"
	virtualDisk    = "/boot/usb.disk0"
	virtualSerial  = "/boot/usb.acm"
	virtualAudio   = "/boot/usb.audio"
	disableHID     = "/boot/disable_hid"
	inittabPath    = "/etc/inittab"

	usbInEndpointLimit  = 6
	usbOutEndpointLimit = 7
)

var usbEndpointCosts = map[string]proto.USBEndpointCost{
	"hid":      {In: 3, Out: 3},
	"keyboard": {In: 1, Out: 1},
	"relative": {In: 1, Out: 1},
	"absolute": {In: 1, Out: 1},
	"network":  {In: 2, Out: 1},
	"disk":     {In: 1, Out: 1},
	"serial":   {In: 2, Out: 1},
	"audio":    {In: 0, Out: 1}, // UAC1 adaptive speaker, no feature-unit interrupt endpoint.
}

type usbComposition struct {
	mode     string
	keyboard bool
	relative bool
	absolute bool
	network  bool
	disk     bool
	serial   bool
	audio    bool
}

func (s *Service) GetVirtualDevice(c *gin.Context) {
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	var rsp proto.Response
	rsp.OkRspWithData(c, getUSBComposition().response())
}

func (s usbComposition) response() *proto.GetVirtualDeviceRsp {
	inUsed, outUsed := s.endpointUsage()
	return &proto.GetVirtualDeviceRsp{
		Keyboard: s.keyboard, Relative: s.relative, Absolute: s.absolute,
		Network: s.network, Disk: s.disk, Serial: s.serial, Audio: s.audio,
		HID: s.keyboard || s.relative || s.absolute, Mode: s.mode,
		Revision: s.revision(),
		Budget:   proto.USBEndpointBudget{InUsed: inUsed, OutUsed: outUsed, InLimit: usbInEndpointLimit, OutLimit: usbOutEndpointLimit},
		Costs:    usbEndpointCosts,
	}
}

func (s usbComposition) revision() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%t:%t:%t:%t:%t:%t:%t", s.mode, s.keyboard, s.relative, s.absolute, s.network, s.disk, s.serial, s.audio))))
}

func (s *Service) SetUSBComposition(c *gin.Context) {
	var req proto.SetUSBCompositionReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid composition")
		return
	}
	candidate := usbComposition{mode: req.Mode, keyboard: *req.Keyboard, relative: *req.Relative, absolute: *req.Absolute, network: *req.Network, disk: *req.Disk, serial: *req.Serial, audio: *req.Audio}
	if err := candidate.validate(); err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	current := getUSBComposition()
	if current.revision() != req.Revision {
		rsp.ErrRsp(c, -5, "USB composition changed; refresh and try again")
		return
	}
	if err := applyLiveUSBComposition(h, current, candidate); err != nil {
		log.Errorf("apply USB composition: %v", err)
		rsp.ErrRsp(c, -3, "failed to apply USB composition")
		return
	}
	rsp.OkRspWithData(c, getUSBComposition().response())
}

// Retain the single-device endpoint for older clients, using the same budget,
// rollback and rebind path as a complete composition.
func (s *Service) UpdateVirtualDevice(c *gin.Context) {
	var req proto.UpdateVirtualDeviceReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid argument")
		return
	}
	if req.Device != "network" && req.Device != "disk" && req.Device != "serial" && req.Device != "audio" {
		rsp.ErrRsp(c, -2, "invalid arguments")
		return
	}
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	current := getUSBComposition()
	candidate := current.with(req.Device, !current.enabled(req.Device))
	if err := candidate.validate(); err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	if err := applyLiveUSBComposition(h, current, candidate); err != nil {
		log.Errorf("update USB device: %v", err)
		rsp.ErrRsp(c, -3, "operation failed")
		return
	}
	rsp.OkRspWithData(c, &proto.UpdateVirtualDeviceRsp{On: candidate.enabled(req.Device)})
}
func retireLegacyACMGetty(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	updated, changed := removeLegacyACMGettyLine(string(data))
	if !changed {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".inittab-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(updated); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	// BusyBox init re-reads inittab on SIGHUP and releases the old ttyGS0
	// getty. This migration is permanent: the browser terminal owns ttyGS0.
	return exec.Command("kill", "-HUP", "1").Run()
}

func removeLegacyACMGettyLine(data string) (string, bool) {
	changed := false
	lines := strings.SplitAfter(data, "\n")
	kept := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "acm::respawn:") && strings.Contains(trimmed, "ttyGS0") {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, ""), changed
}

func getUSBComposition() usbComposition {
	mode, err := hid.GetMode()
	if err != nil {
		// The gadget may not yet be bound while booting or when no USB data cable
		// is attached. The installed default profile is the normal composition.
		mode = hid.ModeNormal
	}

	hidEnabled := !deviceExists(disableHID)
	networkEnabled := deviceExists(virtualNetwork) || deviceExists(virtualNCM)
	diskEnabled := deviceExists(virtualDisk)
	if mode == hid.ModeHidOnly {
		networkEnabled = false
		diskEnabled = false
	}

	return usbComposition{
		mode:     mode,
		keyboard: hidEnabled && !deviceExists("/boot/usb.disable_keyboard"),
		relative: hidEnabled && !deviceExists("/boot/usb.disable_relative"),
		absolute: hidEnabled && !deviceExists("/boot/usb.disable_absolute"),
		network:  networkEnabled,
		disk:     diskEnabled,
		serial:   deviceExists(virtualSerial),
		audio:    mode != hid.ModeHidOnly && deviceExists(virtualAudio),
	}
}

func (s usbComposition) endpointUsage() (int, int) {
	inUsed, outUsed := 0, 0
	for device, enabled := range map[string]bool{
		"keyboard": s.keyboard, "relative": s.relative, "absolute": s.absolute,
		"network": s.network, "disk": s.disk, "serial": s.serial, "audio": s.audio,
	} {
		if enabled {
			cost := usbEndpointCosts[device]
			inUsed += cost.In
			outUsed += cost.Out
		}
	}
	return inUsed, outUsed
}

func (s usbComposition) fitsEndpointBudget() bool {
	inUsed, outUsed := s.endpointUsage()
	return inUsed <= usbInEndpointLimit && outUsed <= usbOutEndpointLimit
}

func (s usbComposition) enabled(device string) bool {
	switch device {
	case "network":
		return s.network
	case "disk":
		return s.disk
	case "audio":
		return s.audio
	case "serial":
		return s.serial
	default:
		return false
	}
}

func (s usbComposition) with(device string, enabled bool) usbComposition {
	switch device {
	case "network":
		s.network = enabled
	case "disk":
		s.disk = enabled
	case "audio":
		s.audio = enabled
		if enabled {
			s.mode = hid.ModeNormal
		}
	case "serial":
		s.serial = enabled
	}
	return s
}

func deviceExists(device string) bool {
	_, err := os.Stat(device)
	if err == nil {
		return true
	}
	if !errors.Is(err, os.ErrNotExist) {
		log.Errorf("check file %s err: %s", device, err)
	}
	return false
}
