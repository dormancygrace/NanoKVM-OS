package netsvc

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
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
)

var ups = &unit{
	name:     "UPS",
	packages: []string{"nut", "nut-openrc"},
	binary:   "/usr/sbin/upsd",
	service:  "nut-upsd",
	pidfile:  "/run/nut/upsd.pid",
	prepare:  prepareUPS,
}

type upsSettings struct {
	// a NUT driver; usbhid-ups serves most USB UPSes
	Driver string `json:"driver"`
	// "auto" for USB; a device such as /dev/ttyS1 for a serial driver
	Port string `json:"port"`
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
	nutPort   = regexp.MustCompile(`^[A-Za-z0-9_./:@-]{1,64}$`)
)

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
	if !nutPort.MatchString(s.Port) {
		return errors.New("the port is \"auto\" or a device such as /dev/ttyS1")
	}
	return nil
}

// The files NUT reads, all written together. upsd listens on the local host
// (the status below reads it there), or on every address when the network
// is on.
func (s upsSettings) files() map[string]string {
	mode := "standalone"
	listen := "LISTEN 127.0.0.1 3493\n"
	if s.Network {
		mode = "netserver"
		// instead of the local address, which it covers: upsd refuses to
		// start when one of its addresses is already taken
		listen = "LISTEN 0.0.0.0 3493\n"
	}
	return map[string]string{
		"nut.conf":   marker + "MODE=" + mode + "\n",
		"ups.conf":   marker + "[" + nutUPS + "]\n\tdriver = " + s.Driver + "\n\tport = " + s.Port + "\n",
		"upsd.conf":  marker + listen,
		"upsd.users": marker + "[" + s.User + "]\n\tpassword = " + s.Password + "\n\tupsmon secondary\n",
	}
}

func writeUPSSettings(s upsSettings) error {
	for name, content := range s.files() {
		// NUT refuses configuration other users can read
		if err := writeFile(filepath.Join(nutDir, name), []byte(content), 0o640, "nut"); err != nil {
			return err
		}
	}
	return nil
}

func readUPSSettings() upsSettings {
	s := upsSettings{Driver: "usbhid-ups", Port: "auto", User: nutUser}
	if !ours(filepath.Join(nutDir, "ups.conf")) {
		return s
	}
	value := func(file, key string) string {
		data, _ := os.ReadFile(filepath.Join(nutDir, file))
		for _, line := range strings.Split(string(data), "\n") {
			name, rest, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && strings.TrimSpace(name) == key {
				return strings.TrimSpace(rest)
			}
		}
		return ""
	}
	if driver := value("ups.conf", "driver"); driver != "" {
		s.Driver = driver
	}
	if port := value("ups.conf", "port"); port != "" {
		s.Port = port
	}
	s.Network = value("nut.conf", "MODE") == "netserver"
	s.Password = value("upsd.users", "password")
	return s
}

// prepareUPS replaces the package's example configuration with ours, once.
// The package's udev rules give the driver (user nut) the USB device of a
// UPS when it is attached; one attached before the install gets them here.
func prepareUPS() error {
	_ = rc(10*time.Second, "udevadm", "trigger", "--subsystem-match=usb", "--action=change")
	if ours(filepath.Join(nutDir, "ups.conf")) {
		return nil
	}
	password, err := secret(20)
	if err != nil {
		return err
	}
	return writeUPSSettings(upsSettings{Driver: "usbhid-ups", Port: "auto", User: nutUser, Password: password})
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

func upsState() any {
	status := upsStatus{Installed: ups.installed(), Drivers: []string{}, Values: map[string]string{}}
	if !status.Installed {
		return status
	}
	status.Enabled, status.Running = ups.enabled(), ups.running()
	status.Settings = readUPSSettings()
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
	rsp.OkRspWithData(c, upsState())
}
func UPSInstall(c *gin.Context)   { ups.handleInstall(c, upsState) }
func UPSUninstall(c *gin.Context) { ups.handleUninstall(c, upsState) }
func UPSEnabled(c *gin.Context)   { ups.handleEnabled(c, upsState) }

type upsSettingsReq struct {
	Driver  string `json:"driver" validate:"required"`
	Port    string `json:"port" validate:"required"`
	Network *bool  `json:"network" validate:"required"`
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
	ups.mutex.Lock()
	defer ups.mutex.Unlock()
	if !ups.installed() {
		rsp.ErrRsp(c, -2, errNotInstalled.Error())
		return
	}
	next := readUPSSettings()
	next.Driver, next.Port, next.Network = req.Driver, strings.TrimSpace(req.Port), *req.Network
	if err := next.validate(); err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	if next.Password == "" || req.NewPassword {
		password, err := secret(20)
		if err != nil {
			rsp.ErrRsp(c, -3, "could not save the settings")
			return
		}
		next.Password = password
	}
	if err := ups.apply(func() error { return writeUPSSettings(next) }); err != nil {
		log.Errorf("UPS settings: %v", err)
		rsp.ErrRsp(c, -3, "could not save the settings")
		return
	}
	rsp.OkRspWithData(c, upsState())
}
