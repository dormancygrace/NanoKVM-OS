// Package bmc runs the server's Redfish service and switches the IPMI daemon
// (nkos-ipmi, OpenRC service nanokvm-ipmi): one settings page, one BMC
// account for both protocols.
package bmc

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/bmc/redfish"
	"NanoKVM-Server/internal/ipmi"
	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/journal"
	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/service/notify"
)

// Status is what the settings page shows. The password never leaves the
// device: PasswordSet tells whether one is stored.
type Status struct {
	Redfish     bool `json:"redfish"`
	IPMI        bool `json:"ipmi"`
	IPMIRunning bool `json:"ipmiRunning"`
	// IPMIDaemon is what the running daemon reports: its sessions, lockouts
	// and last power command. Absent while it does not run or answer.
	IPMIDaemon  *ipmi.Status `json:"ipmiDaemon,omitempty"`
	Username    string       `json:"username"`
	PasswordSet bool         `json:"passwordSet"`
	IPMIPort    int          `json:"ipmiPort"`
	HoldSeconds int          `json:"holdSeconds"`
	// PowerLEDConnected: the power LED header is wired to the host, so its
	// state counts.
	PowerLEDConnected bool `json:"powerLedConnected"`
	// VirtualMedia: Redfish offers the virtual drive too.
	VirtualMedia bool `json:"virtualMedia"`
}

// Settings is a change from the settings page; an empty password keeps the
// stored one.
type Settings struct {
	Redfish     bool   `json:"redfish"`
	IPMI        bool   `json:"ipmi"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	IPMIPort    int    `json:"ipmiPort"`
	HoldSeconds int    `json:"holdSeconds"`
	// PowerLEDConnected, when sent, changes the setting; absent keeps it.
	PowerLEDConnected *bool `json:"powerLedConnected,omitempty"`
	// VirtualMedia, when sent, changes the setting; absent keeps it.
	VirtualMedia *bool `json:"virtualMedia,omitempty"`
}

type Service struct {
	mu         sync.Mutex
	configPath string
	// The service's link in the default runlevel, set by rc-update add.
	runlevelLink string
	// daemon reads the running IPMI daemon's status.
	daemon  ipmiDaemon
	atx     bmc.ATX
	drive   Drive
	sensors redfish.Sensors
	info    redfish.Info
	run     func(name string, args ...string) error

	// The Redfish handler of the current settings; nil while Redfish is off.
	redfish atomic.Pointer[redfish.Handler]
}

func NewService(atx bmc.ATX, drive Drive, sensors redfish.Sensors) *Service {
	s := &Service{
		drive:        drive,
		sensors:      sensors,
		configPath:   bmc.ConfigPath,
		runlevelLink: "/etc/runlevels/default/" + bmc.IPMIService,
		// The settings page polls the status: a short limit for the
		// daemon's answer.
		daemon: newIPMIDaemon(bmc.IPMISocket, 2*time.Second),
		atx:    atx,
		info:   redfish.Info{UUID: deviceUUID(), FirmwareVersion: firmwareVersion()},
		run: func(name string, args ...string) error {
			return exec.Command(name, args...).Run()
		},
	}
	if config, err := bmc.Load(s.configPath); err != nil {
		log.Errorf("BMC settings: %v", err)
	} else {
		s.useRedfish(config)
		if config.IPMI {
			// Off the start path: rc-service waits for nanokvm-app, which
			// is this server starting.
			go s.restoreIPMI()
		}
	}
	return s
}

// restoreIPMI puts the daemon of IPMI that is on back into the default
// runlevel, and starts it, when something other than this service took the
// link away: the removal script of a separate nanokvm-ipmi package does so
// when nanokvm-base takes over its files. With the link in place it does
// nothing, so a server restart leaves the daemon and its sessions alone.
func (s *Service) restoreIPMI() {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, err := bmc.Load(s.configPath)
	if err != nil || !config.IPMI {
		return
	}
	if _, err := os.Lstat(s.runlevelLink); err == nil {
		return
	}
	if err := s.applyIPMI(config); err != nil {
		log.Errorf("IPMI: %v", err)
	}
}

func (s *Service) useRedfish(config bmc.Config) {
	if !config.Redfish || config.Validate() != nil {
		s.redfish.Store(nil)
		return
	}
	power := &bmc.Power{ATX: s.atx, Hold: time.Duration(config.HoldSeconds) * time.Second} // 0: the long press
	h := redfish.New(config.Username, config.Password, s.info, power)
	// The security events cover the BMC account as the web login.
	h.OnLockout = func(addr string, failures int, lock time.Duration) {
		notify.LoginLockedOut(addr, failures, int((lock+time.Minute-1)/time.Minute))
	}
	h.OnLogin = func(user, addr string) {
		// Its own list, apart from a web user of the same name.
		go notify.LoginSucceeded(user+" (Redfish)", addr)
	}
	// The host's sensors are read-only and the host's own: no switch.
	h.Sensors = s.sensors
	if config.VirtualMedia && s.drive != nil {
		h.Media = RedfishMedia(s.drive)
	}
	s.redfish.Store(h)
}

// ServeRedfish answers /redfish/; with Redfish off the service does not exist.
func (s *Service) ServeRedfish(w http.ResponseWriter, r *http.Request) {
	handler := s.redfish.Load()
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	handler.ServeHTTP(w, r.WithContext(journal.WithActor(r.Context(), "Redfish")))
}

func (s *Service) Status() (Status, error) {
	status, err := s.settingsStatus()
	if err != nil || !status.IPMIRunning {
		return status, err
	}
	// Outside s.mu: a daemon slow to answer must not hold up a change of
	// settings. Without an answer the page shows the switch alone.
	var daemon ipmi.Status
	if err := s.daemon.get(context.Background(), "/status", &daemon); err == nil {
		status.IPMIDaemon = &daemon
	}
	return status, nil
}

func (s *Service) settingsStatus() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, err := bmc.Load(s.configPath)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Redfish:           config.Redfish,
		IPMI:              config.IPMI,
		IPMIRunning:       s.run("rc-service", bmc.IPMIService, "status") == nil,
		Username:          config.Username,
		PasswordSet:       config.Password != "",
		IPMIPort:          config.IPMIPort,
		HoldSeconds:       config.HoldSeconds,
		PowerLEDConnected: !config.PowerLEDDisconnected,
		VirtualMedia:      config.VirtualMedia,
	}, nil
}

// Configure stores the settings, switches Redfish at once and starts,
// restarts or stops the IPMI daemon.
func (s *Service) Configure(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, err := bmc.Load(s.configPath)
	if err != nil {
		return err
	}
	config.Redfish, config.IPMI = settings.Redfish, settings.IPMI
	config.Username = settings.Username
	if settings.Password != "" {
		config.Password = settings.Password
	}
	if settings.IPMIPort != 0 {
		config.IPMIPort = settings.IPMIPort
	}
	config.HoldSeconds = settings.HoldSeconds
	if settings.PowerLEDConnected != nil {
		config.PowerLEDDisconnected = !*settings.PowerLEDConnected
	}
	if settings.VirtualMedia != nil {
		config.VirtualMedia = *settings.VirtualMedia
	}
	if err = bmc.Save(s.configPath, config); err != nil {
		return err
	}
	s.useRedfish(config)
	return s.applyIPMI(config)
}

func (s *Service) applyIPMI(config bmc.Config) error {
	if !config.IPMI {
		return s.stopIPMI()
	}
	if err := s.run("rc-update", "add", bmc.IPMIService, "default"); err != nil {
		return errors.New("cannot enable the IPMI service at boot")
	}
	// Without the server's OOM protection, which it would otherwise inherit.
	argv := oomscore.Unprotected("rc-service", bmc.IPMIService, "restart")
	if err := s.run(argv[0], argv[1:]...); err != nil {
		return errors.New("the IPMI service did not start; see the application log")
	}
	return nil
}

// stopIPMI stops the daemon and takes it out of the default runlevel. The
// daemon does not reread its settings: IPMI is off only once its status says
// it is not running, whatever the stop command returned.
func (s *Service) stopIPMI() error {
	_ = s.run("rc-service", bmc.IPMIService, "stop")
	if s.run("rc-service", bmc.IPMIService, "status") == nil {
		return errors.New("the IPMI service did not stop; see the application log")
	}
	if _, err := os.Lstat(s.runlevelLink); err == nil {
		if err = s.run("rc-update", "del", bmc.IPMIService, "default"); err != nil {
			return errors.New("cannot disable the IPMI service at boot")
		}
	}
	return nil
}

// deviceUUID is /etc/machine-id, the device's random identity, as a UUID.
func deviceUUID() string {
	data, err := os.ReadFile("/etc/machine-id")
	h := strings.TrimSpace(string(data))
	if err != nil || len(h) != 32 {
		return ""
	}
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// firmwareVersion is the NanoKVM OS release, for example "3.4.0".
func firmwareVersion() string {
	data, err := os.ReadFile("/etc/nanokvm-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "VERSION="); ok {
			return strings.TrimPrefix(strings.Trim(value, `"`), "v")
		}
	}
	return ""
}
