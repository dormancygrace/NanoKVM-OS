package netsvc

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"

	"github.com/gin-gonic/gin"
)

// UPS: Network UPS Tools reads a UPS attached to the NanoKVM's USB port (in
// the host mode) or to a serial port, and upsd shares its state with the
// computers the UPS feeds, which run their own NUT client. The NanoKVM itself
// does nothing on a power failure: it is not what the UPS protects.

const (
	nutDir     = "/etc/nut"
	nutDrivers = "/usr/lib/nut"
	// the one UPS, as clients name it: ups@<address>
	nutUPS = "ups"
	// the account of the clients on the network
	nutUser = "monitor"
	// where upsd listens while the UPS is not shared
	nutLocal = "127.0.0.1"
)

var ups = &unit{
	name:     "UPS",
	binary:   "/usr/sbin/upsd",
	service:  "nut-upsd",
	pidfile:  "/run/nut/upsd.pid",
	configs:  nutPaths(),
	access:   upsAccess,
	complete: func() bool { _, complete := readUPSSettings(); return complete },
	fresh:    freshUPS,
}

// The files NUT reads that are written here, always together.
func nutPaths() []string {
	paths := []string{}
	for _, name := range []string{"nut.conf", "ups.conf", "upsd.conf", "upsd.users"} {
		paths = append(paths, filepath.Join(nutDir, name))
	}
	return paths
}

type upsSettings struct {
	// a NUT driver; usbhid-ups serves most USB UPSes
	Driver string `json:"driver"`
	// "auto" for USB, or one of the two serial ports
	Port string `json:"port"`
	// USB only: the one device to use, "vendor:product" as in 051d:0002;
	// empty takes the first UPS the driver finds
	USBDevice string `json:"usbDevice"`
	// other computers may read the UPS over the network (port 3493)
	Network bool `json:"network"`
	// what those computers sign in with; generated, not chosen
	User     string `json:"user"`
	Password string `json:"password"`
}

type upsStatus struct {
	Installed bool        `json:"installed"`
	Enabled   bool        `json:"enabled"`
	Running   bool        `json:"running"`
	Foreign   bool        `json:"foreign"`
	Settings  upsSettings `json:"settings"`
	// the drivers the package has, for the settings
	Drivers []string `json:"drivers"`
	// the USB port is in the host mode, which a USB UPS needs
	USBHost bool `json:"usbHost"`
	// what the UPS reports (upsc), empty when the driver has no UPS
	Values map[string]string `json:"values"`
}

var (
	nutDriver = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)
	nutUSBID  = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{4}$`)
)

// The serial ports on the boards' headers; ttyS0 is the system console.
var nutSerialPorts = []string{"/dev/ttyS1", "/dev/ttyS2"}

const nutUSBPort = "auto"

func upsDrivers() []string {
	entries, _ := os.ReadDir(nutDrivers)
	drivers := []string{}
	for _, entry := range entries {
		name := entry.Name()
		// skel is the package's template of a driver, not one
		if entry.IsDir() || !nutDriver.MatchString(name) || name == "skel" {
			continue
		}
		drivers = append(drivers, name)
	}
	sort.Strings(drivers)
	return drivers
}

func (s upsSettings) validate() error {
	if !nutDriver.MatchString(s.Driver) {
		return errors.New("unknown UPS driver")
	}
	if _, err := os.Stat(filepath.Join(nutDrivers, s.Driver)); err != nil {
		return errors.New("unknown UPS driver")
	}
	if s.Port == nutUSBPort {
		if s.USBDevice != "" && !nutUSBID.MatchString(s.USBDevice) {
			return errors.New("the USB device is a vendor and product ID such as 051d:0002")
		}
		return nil
	}
	if s.USBDevice != "" || !slices.Contains(nutSerialPorts, s.Port) {
		return errors.New("the port is USB or one of the serial ports")
	}
	return nil
}

// The content of nutPaths. Each setting is in one file, so files of two
// saves, which a save cut short leaves, are still what the settings then
// show: the driver and its port in ups.conf, the password in upsd.users, and
// sharing in the address upsd listens on, the local host (the status below
// reads it there) or every address. nut.conf does not depend on them: its
// mode only lets the service start.
func (s upsSettings) files() map[string]string {
	listen := nutLocal
	if s.Network {
		// instead of the local address, which it covers: upsd refuses to
		// start when one of its addresses is already taken
		listen = "0.0.0.0"
	}
	device := "[" + nutUPS + "]\n\tdriver = " + s.Driver + "\n\tport = " + s.Port + "\n"
	if vendor, product, ok := strings.Cut(s.USBDevice, ":"); ok {
		device += "\tvendorid = " + vendor + "\n\tproductid = " + product + "\n"
	}
	return map[string]string{
		"nut.conf":   marker + "MODE=netserver\n",
		"ups.conf":   marker + device,
		"upsd.conf":  marker + "LISTEN " + listen + " 3493\n",
		"upsd.users": marker + "[" + s.User + "]\n\tpassword = " + s.Password + "\n\tupsmon secondary\n",
	}
}

func writeUPSSettings(s upsSettings) error {
	files := map[string][]byte{}
	for name, content := range s.files() {
		files[filepath.Join(nutDir, name)] = []byte(content)
	}
	// NUT refuses configuration other users can read
	return writeFiles(files, 0o640, "nut")
}

var upsDefaults = upsSettings{Driver: "usbhid-ups", Port: nutUSBPort, User: nutUser}

// readUPSSettings is what the files say, when all of them are written here
// and make a configuration the settings could have written: a set that is
// short of that is not a configuration yet.
func readUPSSettings() (upsSettings, bool) {
	s := upsDefaults
	text := map[string]string{}
	for _, path := range nutPaths() {
		data, err := os.ReadFile(path)
		if err != nil || !strings.HasPrefix(string(data), marker) {
			return upsDefaults, false
		}
		text[filepath.Base(path)] = string(data)
	}
	value := func(file, key string) string {
		for _, line := range strings.Split(text[file], "\n") {
			name, rest, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && strings.TrimSpace(name) == key {
				return strings.TrimSpace(rest)
			}
		}
		return ""
	}
	s.Driver, s.Port = value("ups.conf", "driver"), value("ups.conf", "port")
	if vendor, product := value("ups.conf", "vendorid"), value("ups.conf", "productid"); vendor != "" && product != "" {
		s.USBDevice = vendor + ":" + product
	}
	// sharing is what upsd does, not what another file says about it
	listens := false
	for _, line := range strings.Split(text["upsd.conf"], "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "LISTEN" {
			listens = true
			s.Network = s.Network || fields[1] != nutLocal
		}
	}
	s.Password = value("upsd.users", "password")
	if !listens || s.Password == "" || s.validate() != nil {
		return upsDefaults, false
	}
	return s, true
}

// upsAccess gives the driver (user nut) what it opens. The package's udev
// rules hand it the USB device of a UPS when one is attached; the trigger
// applies them again to one that is already there. The serial ports belong to
// the group dialout, which the user joins.
func upsAccess() {
	_ = rc(10*time.Second, "udevadm", "trigger", "--subsystem-match=usb", "--action=change")
	_ = rc(10*time.Second, "addgroup", "nut", "dialout")
}

func freshUPS() error {
	s := upsDefaults
	var err error
	if s.Password, err = secret(20); err != nil {
		return err
	}
	return writeUPSSettings(s)
}

func parseUPSValues(out []byte) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if ok && !strings.Contains(key, " ") {
			values[key] = strings.TrimSpace(value)
		}
	}
	return values
}

func upsState(light bool) any {
	status := upsStatus{Installed: ups.installed(), Drivers: []string{}, Values: map[string]string{}}
	if !status.Installed {
		return status
	}
	status.Enabled, status.Running, status.Foreign = ups.state(light)
	status.Settings, _ = readUPSSettings()
	status.Drivers = upsDrivers()
	status.USBHost = fileExists(filepath.Join(hid.USBFlagsDir, "usb.host"))
	if status.Running {
		if out, err := output(5*time.Second, "upsc", nutUPS+"@127.0.0.1"); err == nil {
			status.Values = parseUPSValues(out)
		}
	}
	return status
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func UPSStatus(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, upsState(c.Query("light") == "1"))
}
func UPSEnabled(c *gin.Context) { ups.handleEnabled(c, upsState) }
func UPSReset(c *gin.Context)   { ups.handleReset(c, upsState) }

type upsSettingsReq struct {
	Driver    string `json:"driver" validate:"required"`
	Port      string `json:"port" validate:"required"`
	USBDevice string `json:"usbDevice"`
	Network   *bool  `json:"network" validate:"required"`
	// a new password for the clients
	NewPassword bool `json:"newPassword"`
}

func UPSSettings(c *gin.Context) {
	var req upsSettingsReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	asked := upsSettings{Driver: req.Driver, Port: req.Port, USBDevice: strings.ToLower(req.USBDevice)}
	if err := asked.validate(); err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	ups.handleSettings(c, upsState, func() error {
		next, _ := readUPSSettings()
		next.Driver, next.Port, next.Network = req.Driver, req.Port, *req.Network
		next.USBDevice = strings.ToLower(req.USBDevice)
		if next.Password == "" || req.NewPassword {
			password, err := secret(20)
			if err != nil {
				return err
			}
			next.Password = password
		}
		upsAccess()
		return writeUPSSettings(next)
	})
}
