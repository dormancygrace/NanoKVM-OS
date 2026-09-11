package timeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Servers  []string `json:"servers"`
	Timezone string   `json:"timezone"`
	Format   string   `json:"format"`
}

type Store struct {
	Dir     string
	Restart func() error
	mu      sync.Mutex
}

// Use the system tzdata package for both the OS and application.
func Zones() []string {
	names := []string{"UTC"}
	data, _ := os.ReadFile("/usr/share/zoneinfo/zone.tab")
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && !strings.HasPrefix(f[0], "#") {
			names = append(names, f[2])
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func zoneData(name string) ([]byte, error) {
	if name == "" || strings.Contains(name, "\\") || filepath.IsAbs(name) || filepath.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return nil, errors.New("unknown time zone")
	}
	data, err := os.ReadFile(filepath.Join("/usr/share/zoneinfo", name))
	if err != nil {
		return nil, errors.New("unknown time zone; install tzdata")
	}
	if _, err := time.LoadLocationFromTZData(name, data); err != nil {
		return nil, errors.New("invalid time zone")
	}
	return data, nil
}

var hostname = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func Validate(c Config) error {
	if c.Format != "24" && c.Format != "12" {
		return errors.New("time format must be 24 or 12")
	}
	data, err := zoneData(c.Timezone)
	if err != nil {
		return err
	}
	if _, err = time.LoadLocationFromTZData(c.Timezone, data); err != nil {
		return err
	}
	if len(c.Servers) < 1 || len(c.Servers) > 6 {
		return errors.New("choose between 1 and 6 NTP servers")
	}
	seen := map[string]bool{}
	for _, server := range c.Servers {
		if server == "" || len(server) > 253 || strings.TrimSpace(server) != server {
			return errors.New("invalid NTP server")
		}
		if net.ParseIP(server) == nil {
			for _, label := range strings.Split(strings.TrimSuffix(server, "."), ".") {
				if !hostname.MatchString(label) {
					return errors.New("NTP server must be a hostname or IP address")
				}
			}
		}
		key := strings.ToLower(strings.TrimSuffix(server, "."))
		if seen[key] {
			return errors.New("duplicate NTP server")
		}
		seen[key] = true
	}
	return nil
}

// Keep application updates compatible with older ntpd-based system images.
func (s *Store) Chrony() bool {
	_, err := os.Stat(filepath.Join(s.Dir, "chrony.conf"))
	return err == nil
}
func (s *Store) daemonConfigPath() string {
	name := "ntp.conf"
	if s.Chrony() {
		name = "chrony.conf"
	}
	return filepath.Join(s.Dir, name)
}

func (s *Store) configPath() string { return filepath.Join(s.Dir, "kvm/date-time.json") }

func (s *Store) read() (Config, error) {
	c := Config{Servers: []string{"0.pool.ntp.org", "1.pool.ntp.org", "2.pool.ntp.org", "3.pool.ntp.org"}, Timezone: "UTC", Format: "24"}
	data, err := os.ReadFile(s.configPath())
	if err == nil {
		if err = json.Unmarshal(data, &c); err != nil {
			return c, err
		}
		return c, Validate(c)
	}
	if !os.IsNotExist(err) {
		return c, err
	}
	if tz, err := os.ReadFile(filepath.Join(s.Dir, "timezone")); err == nil {
		if _, err := zoneData(strings.TrimSpace(string(tz))); err == nil {
			c.Timezone = strings.TrimSpace(string(tz))
		}
	}
	if data, err := os.ReadFile(s.daemonConfigPath()); err == nil {
		servers := []string{}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && (f[0] == "server" || f[0] == "pool") {
				servers = append(servers, f[1])
			}
		}
		if len(servers) > 0 {
			c.Servers = servers
		}
	}
	return c, nil
}

func (s *Store) Read() (Config, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.read() }

func ntpConfig(old []byte, servers []string) []byte {
	return serverConfig(old, servers, false)
}

func serverConfig(old []byte, servers []string, chrony bool) []byte {
	lines := []string{}
	for _, host := range servers {
		lines = append(lines, "server "+host+" iburst")
	}
	if len(old) == 0 {
		if chrony {
			old = []byte("driftfile /var/lib/chrony/drift\nmakestep 1.0 3\nrtcsync\nport 0\ncmdport 0\nbindcmdaddress /run/chrony/chronyd.sock\n")
		} else {
			old = []byte("restrict default nomodify nopeer noquery limited kod\nrestrict 127.0.0.1\nrestrict [::1]\n")
		}
	}
	for _, line := range strings.Split(strings.TrimRight(string(old), "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && (f[0] == "server" || f[0] == "pool") {
			continue
		}
		lines = append(lines, line)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".date-time-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Save changes the active time daemon configuration and installs the selected TZif.
// Only a server-list change restarts the time daemon; display preferences never step the clock.
func (s *Store) Save(c Config) error {
	if err := Validate(c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.read()
	if err != nil {
		return err
	}
	tz, _ := zoneData(c.Timezone)
	ntpPath := s.daemonConfigPath()
	ntp, err := os.ReadFile(ntpPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	files := []struct {
		path string
		data []byte
	}{
		{filepath.Join(s.Dir, "localtime"), tz},
		{filepath.Join(s.Dir, "timezone"), []byte(c.Timezone + "\n")},
		{s.configPath(), append(data, '\n')},
	}
	restart := !slices.Equal(old.Servers, c.Servers)
	if restart {
		files = append(files, struct {
			path string
			data []byte
		}{ntpPath, serverConfig(ntp, c.Servers, s.Chrony())})
	}
	type backup struct {
		path    string
		data    []byte
		missing bool
	}
	backups := []backup{}
	rollback := func() error {
		var result error
		for i := len(backups) - 1; i >= 0; i-- {
			b := backups[i]
			var e error
			if b.missing {
				e = os.Remove(b.path)
				if os.IsNotExist(e) {
					e = nil
				}
			} else {
				e = atomicWrite(b.path, b.data)
			}
			result = errors.Join(result, e)
		}
		return result
	}
	for _, f := range files {
		before, e := os.ReadFile(f.path)
		if e != nil && !os.IsNotExist(e) {
			return errors.Join(e, rollback())
		}
		backups = append(backups, backup{f.path, before, os.IsNotExist(e)})
		if e = atomicWrite(f.path, f.data); e != nil {
			return errors.Join(e, rollback())
		}
	}
	if restart && s.Restart != nil {
		if err = s.Restart(); err != nil {
			recovery := rollback()
			recovery = errors.Join(recovery, s.Restart())
			return errors.Join(fmt.Errorf("NTP restart failed: %w", err), recovery)
		}
	}
	return nil
}
