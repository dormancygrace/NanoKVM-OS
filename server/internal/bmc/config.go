// Package bmc holds what the server's Redfish service and the nkos-ipmi
// daemon of the nanokvm-ipmi add-on share: the settings file, the power
// logic and the internal ATX API the daemon uses.
package bmc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"unicode/utf8"
)

const (
	// ConfigPath is root-only: with IPMI on, the password is kept as written,
	// since RMCP+ uses it as the key of its session handshake.
	ConfigPath = "/etc/kvm/bmc/config.json"
	// IPMIDaemonPath is the IPMI add-on's program; its presence means
	// installed.
	IPMIDaemonPath = "/usr/sbin/nkos-ipmi"
	// IPMIService is the IPMI add-on's OpenRC service.
	IPMIService = "nanokvm-ipmi"
	// IPMIPackage is the add-on's APK.
	IPMIPackage = "nanokvm-ipmi"

	DefaultIPMIPort = 623
	// DefaultHoldSeconds is the power menu's long press
	// (DEFAULT_POWER_LONG_DURATION_SECONDS in web/src/pages/desktop/menu/power/power-long.tsx).
	DefaultHoldSeconds = 5
)

// Config is the add-on's settings file.
type Config struct {
	Redfish bool `json:"redfish"`
	IPMI    bool `json:"ipmi"`
	// One BMC account for both protocols, separate from the web accounts.
	Username string `json:"username"`
	Password string `json:"password"`
	IPMIPort int    `json:"ipmiPort"`
	// HoldSeconds is how long a forced power-off holds the power button; 0,
	// the default, is the power menu's long press.
	HoldSeconds int `json:"holdSeconds"`
}

// Default is the configuration of a new installation: both protocols off.
func Default() Config {
	return Config{Username: "admin", IPMIPort: DefaultIPMIPort}
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,16}$`)

// Validate checks the limits of both protocols: IPMI v2.0 user names have at
// most 16 bytes and passwords at most 20.
func (c Config) Validate() error {
	if !usernamePattern.MatchString(c.Username) {
		return errors.New("the user name must have 1-16 letters, digits, '.', '_' or '-'")
	}
	if (c.Redfish || c.IPMI) && len(c.Password) < 8 {
		return errors.New("the password must have at least 8 characters")
	}
	if len(c.Password) > 20 {
		return errors.New("the password must have at most 20 bytes (IPMI limit)")
	}
	if !utf8.ValidString(c.Password) {
		return errors.New("the password must be valid UTF-8")
	}
	for _, r := range c.Password {
		if r < 0x20 || r == 0x7f {
			return errors.New("the password must not contain control characters")
		}
	}
	if c.IPMIPort < 1 || c.IPMIPort > 65535 {
		return errors.New("the IPMI port must be 1-65535")
	}
	if c.HoldSeconds < 0 || c.HoldSeconds > 30 {
		return errors.New("the power button hold must be 1-30 s, or 0 for the power menu's long press")
	}
	return nil
}

// Load reads the settings file; a missing file gives the defaults.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Default(), fmt.Errorf("read %s: %w", path, err)
	}
	return c, nil
}

// Save writes the settings file atomically, readable by root only.
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		if _, err = tmp.Write(append(data, '\n')); err == nil {
			err = tmp.Sync()
		}
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
