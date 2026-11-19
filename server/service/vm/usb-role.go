package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/stream/audio"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// The USB port has one of three roles. S03usbdev reads them from two flags
// next to the composition's (which stay in every role):
//   - gadget: the NanoKVM is a device for the computer, with the composition;
//   - host: devices attach to the NanoKVM, the gadget is unbound (usb.host);
//   - off: the gadget is unbound and the port is a device nobody sees
//     (usb.off). An empty composition in the gadget role is off as well.
const (
	usbRoleGadget = "gadget"
	usbRoleHost   = "host"
	usbRoleOff    = "off"

	usbHostFlag = "usb.host"
	usbOffFlag  = "usb.off"
)

// errUSBNotGadget: the composition is applied to a bound gadget only.
var errUSBNotGadget = errors.New("the USB port is not in gadget mode")

// usbRoleFlags is the role the flags in dir select.
func usbRoleFlags(dir string) string {
	if _, err := os.Lstat(filepath.Join(dir, usbHostFlag)); err == nil {
		return usbRoleHost
	}
	if _, err := os.Lstat(filepath.Join(dir, usbOffFlag)); err == nil {
		return usbRoleOff
	}
	return usbRoleGadget
}

type usbFlagWrite struct {
	name   string
	exists bool
}

// usbRoleWrites is what selecting role does to the two flags, in order. The
// flag of the new role is written before the other one is removed. A change
// between host and off that is cut short then leaves both, and never
// neither: the port stays unbound, where no flag would be the gadget role
// with its keyboard and disk at the next boot.
func usbRoleWrites(role string) []usbFlagWrite {
	host := usbFlagWrite{usbHostFlag, role == usbRoleHost}
	off := usbFlagWrite{usbOffFlag, role == usbRoleOff}
	if off.exists {
		return []usbFlagWrite{off, host}
	}
	return []usbFlagWrite{host, off}
}

type usbRoleStore struct {
	dir     string
	run     func(action string) error
	verify  func(role string) error
	exclude func() error // what must stop before the gadget goes
}

// set writes the flags of role and restarts the USB script. On a failure the
// flags and the running state return to what they were.
func (store usbRoleStore) set(role string) error {
	before := usbRoleFlags(store.dir)
	if before == role {
		return store.verify(role)
	}
	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		return err
	}
	if store.exclude != nil {
		if err := store.exclude(); err != nil {
			return err
		}
	}
	write := func(role string) error {
		for _, flag := range usbRoleWrites(role) {
			if err := writeUSBFile(filepath.Join(store.dir, flag.name), usbFileSnapshot{exists: flag.exists, mode: 0o644}); err != nil {
				return err
			}
		}
		return nil
	}
	apply := func(role string) error {
		if err := write(role); err != nil {
			return err
		}
		if err := store.run("stop_start"); err != nil {
			return err
		}
		return store.verify(role)
	}
	if err := apply(role); err != nil {
		if back := apply(before); back != nil {
			return fmt.Errorf("%w; returning to the %s role also failed: %v", err, before, back)
		}
		return err
	}
	return nil
}

// verifyUSBRoleAt compares the kernel's state with a role: the controller's
// role in roleGlob (the role switch) and the gadget's binding in gadget.
func verifyUSBRoleAt(roleGlob, gadget, role string, s usbComposition) error {
	want := "device"
	if role == usbRoleHost {
		want = "host"
	}
	paths, _ := filepath.Glob(roleGlob)
	if len(paths) == 0 {
		if role == usbRoleHost {
			return errors.New("this kernel has no USB role switch")
		}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// "none" is the default role, a device.
		got := strings.TrimSpace(string(data))
		if got == "none" {
			got = "device"
		}
		if got != want {
			return fmt.Errorf("the USB controller is a %s, not a %s", got, want)
		}
	}
	udc, err := os.ReadFile(filepath.Join(gadget, "UDC"))
	if err != nil {
		return err
	}
	bound := strings.TrimSpace(string(udc)) != ""
	if bound != (role == usbRoleGadget && !s.empty()) {
		return errors.New("the USB gadget binding does not match the role")
	}
	return nil
}

const (
	usbRoleSwitchGlob = "/sys/class/usb_role/*/role"
	usbGadgetRoot     = "/sys/kernel/config/usb_gadget/g0"
)

// setLiveUSBRole is called with the HID lock held.
func setLiveUSBRole(h *hid.Hid, role string) error {
	store := usbRoleStore{
		dir: hid.USBFlagsDir,
		run: func(action string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, "sh", hid.USBDevScript, action).CombinedOutput()
			if err != nil {
				return fmt.Errorf("USB %s: %w: %s", action, err, output)
			}
			return nil
		},
		verify: func(role string) error {
			return verifyUSBRoleAt(usbRoleSwitchGlob, usbGadgetRoot, role, getUSBComposition())
		},
		exclude: audio.Stop,
	}
	h.CloseNoLock()
	defer h.OpenNoLock()
	networkBefore := usbRoleFlags(hid.USBFlagsDir) == usbRoleGadget
	if err := store.set(role); err != nil {
		return err
	}
	// The USB network exists with the bound gadget only: its address, DHCP
	// and input filter follow, as after a composition change.
	if networkBefore != (role == usbRoleGadget) && getUSBComposition().network {
		if err := applyUSBNetwork(); err != nil {
			log.Errorf("USB network after role change: %v", err)
		}
	}
	return nil
}

func (s *Service) SetUSBRole(c *gin.Context) {
	var req proto.SetUSBRoleReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid role")
		return
	}
	h := hid.GetHid()
	h.Lock()
	defer h.Unlock()
	if err := setLiveUSBRole(h, req.Role); err != nil {
		log.Errorf("set USB role %s: %v", req.Role, err)
		rsp.ErrRsp(c, -3, "failed to change the USB role")
		return
	}
	rsp.OkRspWithData(c, getUSBComposition().response())
}

var usbHostDeviceName = regexp.MustCompile(`^[0-9]+-[0-9]+(\.[0-9]+)*$`)

// usbHostDevicesAt lists the devices attached to the NanoKVM as a host:
// the entries of /sys/bus/usb/devices that are devices on a port (not root
// hubs, not interfaces).
func usbHostDevicesAt(root string) []proto.USBHostDevice {
	entries, err := os.ReadDir(root)
	if err != nil {
		return []proto.USBHostDevice{}
	}
	read := func(dir, name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	devices := []proto.USBHostDevice{}
	for _, entry := range entries {
		name := entry.Name()
		if !usbHostDeviceName.MatchString(name) {
			continue
		}
		dir := filepath.Join(root, name)
		vendor := read(dir, "idVendor")
		if vendor == "" {
			continue
		}
		device := proto.USBHostDevice{
			Port: name, VendorID: vendor, ProductID: read(dir, "idProduct"),
			Manufacturer: read(dir, "manufacturer"), Product: read(dir, "product"),
			Speed: read(dir, "speed"), Class: read(dir, "bDeviceClass"), MaxPower: read(dir, "bMaxPower"),
		}
		// A device of class 00 names its classes per interface.
		interfaces, _ := filepath.Glob(filepath.Join(root, name+":*", "bInterfaceClass"))
		seen := map[string]bool{}
		for _, path := range interfaces {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if class := strings.TrimSpace(string(data)); !seen[class] {
				seen[class] = true
				device.InterfaceClasses = append(device.InterfaceClasses, class)
			}
		}
		sort.Strings(device.InterfaceClasses)
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Port < devices[j].Port })
	return devices
}

func (s *Service) GetUSBHostDevices(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, &proto.GetUSBHostDevicesRsp{Devices: usbHostDevicesAt("/sys/bus/usb/devices"), PortPower: usbPortPower()})
}

// usbPortPower is what the port supplies to a device attached to it.
//   - none: the PCIe board, where every power input is behind an ideal diode;
//   - board: a Cube, powered through its other port: the data port's VBUS is
//     the board's 5 V rail;
//   - shared: a Lite, whose one USB-C port is also its power input. It is a
//     Cube's core board without the OLED board, which is how it is told.
func usbPortPower() string {
	hw, _ := os.ReadFile("/etc/kvm/hw")
	if strings.TrimSpace(string(hw)) == "pcie" {
		return "none"
	}
	if _, err := os.Stat(OLEDExistFile); err != nil {
		return "shared"
	}
	return "board"
}
