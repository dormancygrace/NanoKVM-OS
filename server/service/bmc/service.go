// Package bmc runs the server's Redfish service and manages the IPMI add-on
// (nanokvm-ipmi): one settings page, one BMC account for both protocols.
package bmc

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/bmc"
	"NanoKVM-Server/internal/bmc/redfish"
	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/service/extensions/apkpkg"
)

// Status is what the settings page shows. The password never leaves the
// device: PasswordSet tells whether one is stored.
type Status struct {
	Redfish       bool   `json:"redfish"`
	IPMI          bool   `json:"ipmi"`
	IPMIInstalled bool   `json:"ipmiInstalled"`
	IPMIRunning   bool   `json:"ipmiRunning"`
	Username      string `json:"username"`
	PasswordSet   bool   `json:"passwordSet"`
	IPMIPort      int    `json:"ipmiPort"`
	HoldSeconds   int    `json:"holdSeconds"`
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
}

type Service struct {
	mu         sync.Mutex
	configPath string
	daemonPath string
	atx        bmc.ATX
	info       redfish.Info
	run        func(name string, args ...string) error
	install    func() error
	remove     func() error

	// The Redfish handler of the current settings; nil while Redfish is off.
	redfish atomic.Pointer[redfish.Handler]
}

func NewService(atx bmc.ATX) *Service {
	s := &Service{
		configPath: bmc.ConfigPath,
		daemonPath: bmc.IPMIDaemonPath,
		atx:        atx,
		info:       redfish.Info{UUID: deviceUUID(), FirmwareVersion: firmwareVersion()},
		run: func(name string, args ...string) error {
			return exec.Command(name, args...).Run()
		},
		install: func() error { return apkpkg.Install(bmc.IPMIPackage) },
		remove:  func() error { return apkpkg.Run("remove", bmc.IPMIPackage) },
	}
	if config, err := bmc.Load(s.configPath); err != nil {
		log.Errorf("BMC settings: %v", err)
	} else {
		s.useRedfish(config)
	}
	return s
}

func (s *Service) useRedfish(config bmc.Config) {
	if !config.Redfish || config.Validate() != nil {
		s.redfish.Store(nil)
		return
	}
	power := &bmc.Power{ATX: s.atx, Hold: time.Duration(config.HoldSeconds) * time.Second} // 0: the long press
	s.redfish.Store(redfish.New(config.Username, config.Password, s.info, power))
}

// ServeRedfish answers /redfish/; with Redfish off the service does not exist.
func (s *Service) ServeRedfish(w http.ResponseWriter, r *http.Request) {
	handler := s.redfish.Load()
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	handler.ServeHTTP(w, r)
}

func (s *Service) ipmiInstalled() bool {
	info, err := os.Stat(s.daemonPath)
	return err == nil && info.Mode().IsRegular()
}

func (s *Service) Status() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, err := bmc.Load(s.configPath)
	if err != nil {
		return Status{}, err
	}
	installed := s.ipmiInstalled()
	return Status{
		Redfish:       config.Redfish,
		IPMI:          config.IPMI,
		IPMIInstalled: installed,
		IPMIRunning:   installed && s.run("rc-service", bmc.IPMIService, "status") == nil,
		Username:      config.Username,
		PasswordSet:   config.Password != "",
		IPMIPort:      config.IPMIPort,
		HoldSeconds:   config.HoldSeconds,
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
	if settings.IPMI && !s.ipmiInstalled() {
		return errors.New("install the IPMI add-on first")
	}
	config.Redfish, config.IPMI = settings.Redfish, settings.IPMI
	config.Username = settings.Username
	if settings.Password != "" {
		config.Password = settings.Password
	}
	config.IPMIPort, config.HoldSeconds = settings.IPMIPort, settings.HoldSeconds
	if err = bmc.Save(s.configPath, config); err != nil {
		return err
	}
	s.useRedfish(config)
	return s.applyIPMI(config)
}

func (s *Service) applyIPMI(config bmc.Config) error {
	if !s.ipmiInstalled() {
		return nil
	}
	if !config.IPMI {
		_ = s.run("rc-service", bmc.IPMIService, "stop")
		_ = s.run("rc-update", "del", bmc.IPMIService, "default")
		return nil
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

// InstallIPMI adds the package; IPMI stays off until turned on.
func (s *Service) InstallIPMI() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ipmiInstalled() {
		return nil
	}
	return s.install()
}

// UninstallIPMI stops the daemon and removes the package. The settings stay.
func (s *Service) UninstallIPMI() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ipmiInstalled() {
		return nil
	}
	_ = s.run("rc-service", bmc.IPMIService, "stop")
	_ = s.run("rc-update", "del", bmc.IPMIService, "default")
	if err := s.remove(); err != nil {
		return err
	}
	if config, err := bmc.Load(s.configPath); err == nil && config.IPMI {
		config.IPMI = false
		_ = bmc.Save(s.configPath, config)
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
