package rustdesk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/service/vm"

	log "github.com/sirupsen/logrus"
)

const Package = "nanokvm-rustdesk"

var ConfigDir = "/etc/nanokvm-rustdesk"
var ConfigFile = ConfigDir + "/config.json"

type Config struct {
	AudioEnabled *bool `json:"audio_enabled,omitempty"`
	LocalPointer *bool `json:"local_pointer,omitempty"`
	// The address of the direct listener; the daemon listens on the loopback
	// one without it.
	Listen       string `json:"listen_address,omitempty"`
	WebRTC       bool   `json:"webrtc_enabled,omitempty"`
	Enabled      bool   `json:"service_enabled"`
	Official     bool   `json:"use_official_id_server"`
	Rendezvous   string `json:"rendezvous_server"`
	Relay        string `json:"relay_server"`
	Key          string `json:"server_key"`
	Password     string `json:"password,omitempty"`
	PasswordMode string `json:"password_mode"`
	Codec        string `json:"codec"`
	MaxClients   int    `json:"max_clients"`
}
type Status struct {
	SupportsTransportSettings bool            `json:"supports_transport_settings"`
	SupportsAudio             bool            `json:"supports_audio"`
	SupportsAudioSettings     bool            `json:"supports_audio_settings"`
	PointerSettings           bool            `json:"supports_pointer_settings"`
	USBAudioEnabled           bool            `json:"usb_audio_enabled"`
	Version                   string          `json:"version,omitempty"`
	RustDeskVersion           string          `json:"rustdesk_version,omitempty"`
	SourceURL                 string          `json:"source_url,omitempty"`
	Running                   bool            `json:"running"`
	Config                    Config          `json:"config"`
	HasPassword               bool            `json:"has_password"`
	TemporaryPassword         string          `json:"temporary_password,omitempty"`
	ID                        string          `json:"id"`
	Runtime                   json.RawMessage `json:"runtime,omitempty"`
}
type Service struct {
	mu           sync.Mutex
	bridge       *Bridge
	run          func(context.Context, string, ...string) ([]byte, error)
	passwordFile string
	upstreamFile string
	sourceFile   string
	startedFile  string
	// USB for RustDesk: its preset when it turns on, audio added when its
	// sound turns on while it runs, the previous composition when it turns off
	enableUSB  func(bool) error
	ensureUSB  func(bool) error
	restoreUSB func() error
}

func NewService(b *Bridge) *Service {
	s := &Service{bridge: b, run: runCommand, passwordFile: RuntimeDir + "/temporary-password", upstreamFile: "/usr/share/nanokvm-rustdesk/upstream.json", sourceFile: "/usr/share/nanokvm-rustdesk/source.json", startedFile: "/run/openrc/started/" + Package, enableUSB: vm.EnableRemoteAccessUSB, ensureUSB: vm.EnsureRemoteAccessUSB, restoreUSB: vm.RestoreUSBAfterRemoteAccess}
	b.mu.Lock()
	b.prepareUSB = s.PrepareUSB
	b.mu.Unlock()
	return s
}

func audioRequested(c Config) bool { return c.AudioEnabled == nil || *c.AudioEnabled }

// Called once by the enabled daemon before it accepts remote connections,
// including after cold boot, package upgrade and service restarts. It leaves
// the USB composition alone: USB changes when the user turns RustDesk or its
// sound on or RustDesk off (Configure), and a later choice in USB settings,
// such as turning USB off, must survive restarts.
func (s *Service) PrepareUSB() error {
	c, err := readConfig()
	if err != nil {
		return err
	}
	if !c.Enabled {
		return errors.New("RustDesk remote access is disabled")
	}
	_, err = os.Stat(Binary)
	return err
}
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	argv := append([]string{name}, args...)
	if name == "rc-service" && len(args) > 1 && (args[1] == "start" || args[1] == "restart") {
		// The daemon must not inherit the server's OOM protection.
		argv = oomscore.Unprotected(name, args...)
	}
	data, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %s", name, strings.TrimSpace(string(data[:min(len(data), 2048)])))
	}
	return data, nil
}
func (s *Service) command(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return s.run(ctx, name, args...)
}
func (s *Service) statusCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.run(ctx, name, args...)
}

// The default openrc-run status succeeds only in the started state, so a
// service without its started link is stopped and needs no rc-service run.
// rc-service still decides for a started service, which may have crashed.
func (s *Service) daemonRunning() bool {
	if _, err := os.Lstat(filepath.Dir(s.startedFile)); err == nil {
		if _, err = os.Lstat(s.startedFile); errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	_, err := s.statusCommand("rc-service", Package, "status")
	return err == nil
}

func defaultConfig() Config {
	return Config{Official: true, Codec: "auto", MaxClients: 1, PasswordMode: "temporary"}
}
func readConfig() (Config, error) {
	c := defaultConfig()
	data, err := os.ReadFile(ConfigFile)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return c, err
	}
	// Existing installs retain their permanent password until the user changes mode.
	if _, exists := fields["password_mode"]; !exists && c.Password != "" {
		c.PasswordMode = "permanent"
	}
	// An older codec preference never overrides the shared device codec.
	c.Codec = "auto"
	return c, nil
}
func (s *Service) Status() (Status, error) {
	c, err := readConfig()
	if err != nil {
		return Status{}, err
	}
	status := Status{Config: c, HasPassword: c.Password != ""}
	status.Config.Password = ""
	status.Version, status.SourceURL = s.source()
	// This metadata belongs to the installed daemon, including when stopped.
	if data, readErr := os.ReadFile(s.upstreamFile); readErr == nil {
		var upstream struct {
			Version  string `json:"rustdesk_version"`
			Features struct {
				TransportSettings bool `json:"transport_settings"`
				Audio             bool `json:"audio"`
				AudioSettings     bool `json:"audio_settings"`
				PointerSettings   bool `json:"pointer_settings"`
			} `json:"features"`
		}
		if json.Unmarshal(data, &upstream) == nil && regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(upstream.Version) {
			status.RustDeskVersion = upstream.Version
			status.SupportsTransportSettings = upstream.Features.TransportSettings
			status.SupportsAudio = upstream.Features.Audio
			status.SupportsAudioSettings = upstream.Features.AudioSettings
			status.PointerSettings = upstream.Features.PointerSettings
			status.USBAudioEnabled = status.SupportsAudio && s.bridge.audioEnabled()
		}
	}
	status.Running = s.daemonRunning()
	if data, readErr := os.ReadFile(ConfigDir + "/settings-output.json"); readErr == nil {
		var fields map[string]string
		if json.Unmarshal(data, &fields) == nil {
			status.ID = fields["rustdesk_id"]
		}
	}
	if status.Running {
		if c.PasswordMode == "temporary" {
			if password, readErr := os.ReadFile(s.passwordFile); readErr == nil &&
				regexp.MustCompile("^[A-HJ-NP-Z2-9]{10}$").Match(password) {
				status.TemporaryPassword = string(password)
			}
		}
		if data, readErr := os.ReadFile(RuntimeDir + "/status.json"); readErr == nil && json.Valid(data) {
			status.Runtime = data
		}
	}
	return status, nil
}

// source returns the package version and the public source location
// recorded by the APK: a legacy release archive, or the Git tree of the
// version's tag. The device stores and serves no source.
func (s *Service) source() (version, sourceURL string) {
	data, err := os.ReadFile(s.sourceFile)
	if err != nil {
		return "", ""
	}
	var source struct {
		URL     string `json:"url"`
		Version string `json:"package_version"`
	}
	if json.Unmarshal(data, &source) != nil {
		return "", ""
	}
	parsed, err := url.Parse(source.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Path == "" {
		return source.Version, ""
	}
	return source.Version, source.URL
}

func serverAddress(value string, port string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 255 || strings.ContainsAny(value, "/ \t\r\n@?#") {
		return "", errors.New("server must be a hostname or IP with an optional port")
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return net.JoinHostPort(ip.String(), port), nil
	}
	if !strings.Contains(value, ":") {
		value = net.JoinHostPort(value, port)
	}
	host, p, err := net.SplitHostPort(value)
	if err != nil || host == "" || !regexp.MustCompile("^[0-9]{1,5}$").MatchString(p) {
		return "", errors.New("invalid server address")
	}
	var n int
	fmt.Sscanf(p, "%d", &n)
	if n < 1 || n > 65535 {
		return "", errors.New("invalid server port")
	}
	if net.ParseIP(host) == nil && !regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9.-]*$").MatchString(host) {
		return "", errors.New("invalid server hostname")
	}
	return value, nil
}
func validateConfig(c *Config) error {
	if c.Codec != "" && c.Codec != "auto" && c.Codec != "h264" && c.Codec != "h265" {
		return errors.New("codec must be auto, h264 or h265")
	}
	c.Codec = "auto"
	if c.MaxClients < 1 || c.MaxClients > 8 {
		return errors.New("client limit must be between 1 and 8")
	}
	if c.Listen != "" && net.ParseIP(c.Listen) == nil {
		return errors.New("listen address must be an IP address")
	}
	if c.PasswordMode != "temporary" && c.PasswordMode != "permanent" {
		return errors.New("password mode must be temporary or permanent")
	}
	if (c.PasswordMode == "permanent" || c.Password != "") &&
		(len(c.Password) < 8 || len(c.Password) > 64) {
		return errors.New("password must contain 8 to 64 bytes")
	}
	var err error
	if c.Rendezvous, err = serverAddress(c.Rendezvous, "21116"); err != nil {
		return err
	}
	if c.Relay, err = serverAddress(c.Relay, "21117"); err != nil {
		return err
	}
	if !c.Official && c.Rendezvous == "" {
		return errors.New("custom ID server is required")
	}
	if c.Key != "" {
		key, err := base64.StdEncoding.DecodeString(c.Key)
		if err != nil || len(key) != 32 {
			return errors.New("server key must be a base64-encoded 32-byte public key")
		}
	}
	return nil
}
func writeConfig(c Config) error {
	if err := os.MkdirAll(ConfigDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(ConfigDir, ".config-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, ConfigFile); err != nil {
		return err
	}
	dir, err := os.Open(ConfigDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Service) supportsTransportSettings() bool {
	data, err := os.ReadFile(s.upstreamFile)
	if err != nil {
		return false
	}
	var metadata struct {
		Features struct {
			TransportSettings bool `json:"transport_settings"`
		} `json:"features"`
	}
	return json.Unmarshal(data, &metadata) == nil && metadata.Features.TransportSettings
}

// wantsWebRTC reports whether the daemon accepts WebRTC connections: whenever
// the installed daemon supports it. The client chooses the path: a request
// with a WebRTC offer is answered over WebRTC, one without over TCP or relay.
// The video transport of the device is the browsers' matter only.
func (s *Service) wantsWebRTC() bool {
	return s.supportsTransportSettings()
}

// AcceptWebRTC turns webrtc_enabled on in a configuration written while
// WebRTC followed the device transport, restarting a running daemon.
func (s *Service) AcceptWebRTC() {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// A daemon that was never configured is off; Configure sets the
		// option when the user sets RustDesk up.
		if _, err := os.Stat(ConfigFile); err != nil {
			return
		}
		c, err := readConfig()
		if err != nil || c.WebRTC == s.wantsWebRTC() {
			return
		}
		if err = s.configureLocked(c); err != nil {
			log.Errorf("RustDesk: accept WebRTC: %s", err)
		}
	}()
}

// runlevelLink is the service's link in the default runlevel: Configure adds
// it when RustDesk turns on and removes it when it turns off.
var runlevelLink = "/etc/runlevels/default/" + Package

// SwitchOffStale turns RustDesk off in its configuration, and nothing else,
// when the configuration says on but is left from an add-on that was removed:
// selected is false (the package was never chosen, see the caller), or the
// service has no runlevel link, which the add-on's removal deleted. It runs
// before anything reads the configuration (Monitor, AcceptWebRTC), which
// would otherwise open the bridge and start the daemon with its old ID.
func SwitchOffStale(selected bool) error {
	if _, err := os.Stat(ConfigFile); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	c, err := readConfig()
	if err != nil || !c.Enabled {
		return err
	}
	if _, err = os.Lstat(runlevelLink); err == nil && selected {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	c.Enabled = false
	log.Infof("RustDesk: switched off the setting of the removed add-on")
	return writeConfig(c)
}

func (s *Service) supportsAudioSettings() bool {
	data, err := os.ReadFile(s.upstreamFile)
	if err != nil {
		return false
	}
	var metadata struct {
		Features struct {
			AudioSettings bool `json:"audio_settings"`
		} `json:"features"`
	}
	return json.Unmarshal(data, &metadata) == nil && metadata.Features.AudioSettings
}

// A daemon from before the setting refuses a configuration that has it.
func (s *Service) supportsPointerSettings() bool {
	data, err := os.ReadFile(s.upstreamFile)
	if err != nil {
		return false
	}
	var metadata struct {
		Features struct {
			PointerSettings bool `json:"pointer_settings"`
		} `json:"features"`
	}
	return json.Unmarshal(data, &metadata) == nil && metadata.Features.PointerSettings
}
func (s *Service) Configure(candidate Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configureLocked(candidate)
}

// configureLocked keeps reads and writes in the same service transaction.
func (s *Service) configureLocked(candidate Config) error {
	current, err := readConfig()
	if err != nil {
		return err
	}
	if candidate.PasswordMode == "" {
		candidate.PasswordMode = current.PasswordMode
	}
	if candidate.AudioEnabled == nil {
		candidate.AudioEnabled = current.AudioEnabled
	}
	if candidate.LocalPointer == nil {
		candidate.LocalPointer = current.LocalPointer
	}
	if candidate.Listen == "" {
		candidate.Listen = current.Listen
	}
	if candidate.PasswordMode == "temporary" || candidate.Password == "" {
		candidate.Password = current.Password
	}
	if err = validateConfig(&candidate); err != nil {
		return err
	}
	// WebRTC is not a setting: the daemon accepts it whenever it can.
	candidate.WebRTC = s.wantsWebRTC()
	if candidate.LocalPointer != nil && !s.supportsPointerSettings() {
		return errors.New("update RustDesk before configuring the client pointer")
	}
	if candidate.AudioEnabled != nil && !s.supportsAudioSettings() {
		return errors.New("update RustDesk before configuring sound transmission")
	}
	// Only turning RustDesk on or off, or its sound on, changes USB; saving
	// other settings keeps what the user chose in USB settings. Turning on
	// switches to the RustDesk preset and turning off puts back what it
	// replaced.
	turningOn := candidate.Enabled && !current.Enabled
	soundOn := candidate.Enabled && current.Enabled && audioRequested(candidate) && !audioRequested(current)
	turningOff := !candidate.Enabled && current.Enabled
	switch {
	case turningOn:
		err = s.enableUSB(audioRequested(candidate))
	case soundOn:
		err = s.ensureUSB(true)
	}
	if err != nil {
		return fmt.Errorf("prepare USB for RustDesk: %w", err)
	}
	if _, err = s.command("rc-service", Package, "status"); err == nil {
		if _, err = s.command("rc-service", Package, "stop"); err != nil {
			return err
		}
	}
	if err = writeConfig(candidate); err != nil {
		return err
	}
	if candidate.Enabled {
		if err = s.bridge.Start(); err != nil {
			return err
		}
		if _, err = s.command("rc-update", "add", Package, "default"); err != nil {
			return err
		}
		_, err = s.command("rc-service", Package, "start")
	} else {
		// rc-update del fails if the disabled service has never been enabled.
		data, showErr := s.command("rc-update", "show", "default")
		if showErr != nil {
			return showErr
		}
		for _, field := range strings.Fields(string(data)) {
			if field == Package {
				_, err = s.command("rc-update", "del", Package, "default")
				break
			}
		}
		// RustDesk is off whatever happens to USB
		if err == nil && turningOff {
			if restoreErr := s.restoreUSB(); restoreErr != nil {
				err = fmt.Errorf("RustDesk is off; restore USB: %w", restoreErr)
			}
		}
	}
	return err
}
func (s *Service) Action(action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch action {
	case "regenerate-password":
		c, err := readConfig()
		if err != nil {
			return err
		}
		if c.PasswordMode != "temporary" || !c.Enabled {
			return errors.New("enable temporary password mode first")
		}
		if _, err = s.command("rc-service", Package, "status"); err != nil {
			return errors.New("start RustDesk before generating a new password")
		}
		_, err = s.command("rc-service", Package, "restart")
		return err
	default:
		return errors.New("unknown RustDesk action")
	}
}
