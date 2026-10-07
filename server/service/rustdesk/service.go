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
	"regexp"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/service/vm"
)

const Package = "nanokvm-rustdesk"

var ConfigDir = "/etc/nanokvm-rustdesk"
var ConfigFile = ConfigDir + "/config.json"

type Config struct {
	AudioEnabled *bool  `json:"audio_enabled,omitempty"`
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
	USBAudioEnabled           bool            `json:"usb_audio_enabled"`
	Installed                 bool            `json:"installed"`
	Version                   string          `json:"version,omitempty"`
	RustDeskVersion           string          `json:"rustdesk_version,omitempty"`
	SourceURL                 string          `json:"source_url,omitempty"`
	UpdateVersion             string          `json:"update_version,omitempty"`
	Available                 bool            `json:"available"`
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
	ensureUSB    func(bool) error
}

func NewService(b *Bridge) *Service {
	s := &Service{bridge: b, run: runCommand, passwordFile: RuntimeDir + "/temporary-password", upstreamFile: "/usr/share/nanokvm-rustdesk/upstream.json", sourceFile: "/usr/share/nanokvm-rustdesk/source.json", ensureUSB: vm.EnsureRemoteAccessUSB}
	b.mu.Lock()
	b.prepareUSB = s.PrepareUSB
	b.mu.Unlock()
	return s
}

func audioRequested(c Config) bool { return c.AudioEnabled == nil || *c.AudioEnabled }

// Called once by the enabled daemon before it accepts remote connections,
// including after cold boot, package upgrade and service restarts. It leaves
// the USB composition alone: USB functions are enabled when the user turns
// RustDesk or its sound on (Configure), and a later choice in USB settings,
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
	cmd := exec.CommandContext(ctx, name, args...)
	data, err := cmd.CombinedOutput()
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
	// Legacy per-add-on preferences never override the shared device codec.
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
	_, err = s.statusCommand("apk", "info", "-e", Package)
	status.Installed = err == nil
	// Status polling reads the existing indexes; repository refresh belongs to package management.
	_, status.Available = s.repositoryVersion(false)
	if status.Installed {
		status.SourceURL = s.SourceURL()
		// This metadata belongs to the installed daemon, including when stopped.
		// Older packages without metadata remain unknown rather than inheriting
		// a possibly incorrect version from the web application.
		if data, readErr := os.ReadFile(s.upstreamFile); readErr == nil {
			var upstream struct {
				Version  string `json:"rustdesk_version"`
				Features struct {
					TransportSettings bool `json:"transport_settings"`
					Audio             bool `json:"audio"`
					AudioSettings     bool `json:"audio_settings"`
				} `json:"features"`
			}
			if json.Unmarshal(data, &upstream) == nil && regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(upstream.Version) {
				status.RustDeskVersion = upstream.Version
				status.SupportsTransportSettings = upstream.Features.TransportSettings
				status.SupportsAudio = upstream.Features.Audio
				status.SupportsAudioSettings = upstream.Features.AudioSettings
				status.USBAudioEnabled = status.SupportsAudio && s.bridge.audioEnabled()
			}
		}
		if version, versionErr := s.statusCommand("apk", "info", "-e", "-v", Package); versionErr == nil {
			value := strings.TrimSpace(string(version))
			if strings.HasPrefix(value, Package+"-") {
				status.Version = strings.TrimPrefix(value, Package+"-")
			}
		}
		if status.Version != "" {
			status.UpdateVersion, _ = s.repositoryVersion(true)
		}
		_, err = s.statusCommand("rc-service", Package, "status")
		status.Running = err == nil
	}
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

// SourceURL returns the immutable public source archive recorded by the APK.
// No source archive is stored or served by the device.
func (s *Service) SourceURL() string {
	data, err := os.ReadFile(s.sourceFile)
	if err != nil {
		return ""
	}
	var source struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(data, &source) != nil {
		return ""
	}
	parsed, err := url.Parse(source.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Path == "" {
		return ""
	}
	return source.URL
}

// APK reports newer repository versions using its own upgrade selection.
func (s *Service) repositoryVersion(upgradable bool) (string, bool) {
	args := []string{"query", "--no-network"}
	if !upgradable {
		args = append(args, "--from=repositories")
	}
	// Upgrade selection needs the installed database as well as the repositories.
	args = append(args, "--format=json", "--fields=name,version")
	if upgradable {
		args = append(args, "--upgradable")
	}
	data, err := s.statusCommand("apk", append(args, Package)...)
	if err != nil {
		return "", false
	}
	var packages []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &packages) != nil {
		return "", false
	}
	for _, p := range packages {
		if p.Name == Package && p.Version != "" {
			return p.Version, true
		}
	}
	return "", false
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
func (s *Service) Configure(candidate Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if candidate.PasswordMode == "temporary" || candidate.Password == "" {
		candidate.Password = current.Password
	}
	if err = validateConfig(&candidate); err != nil {
		return err
	}
	if _, err = s.command("apk", "info", "-e", Package); err != nil {
		return errors.New("install the RustDesk package first")
	}
	if candidate.WebRTC && !s.supportsTransportSettings() {
		return errors.New("update the RustDesk add-on before enabling WebRTC")
	}
	if candidate.AudioEnabled != nil && !s.supportsAudioSettings() {
		return errors.New("update the RustDesk add-on before configuring sound transmission")
	}
	// Only turning RustDesk or its sound on changes USB; saving other settings
	// keeps what the user chose in USB settings.
	turningOn := candidate.Enabled && (!current.Enabled || audioRequested(candidate) && !audioRequested(current))
	if turningOn {
		if err = s.ensureUSB(audioRequested(candidate)); err != nil {
			return fmt.Errorf("prepare USB for RustDesk: %w", err)
		}
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
	case "install":
		_, err := s.command("apk", "add", Package)
		return err
	case "upgrade":
		_, err := s.command("apk", "upgrade", Package)
		return err
	case "remove":
		if _, err := s.command("apk", "del", Package); err != nil {
			return err
		}
		s.bridge.Stop()
		return nil
	default:
		return errors.New("unknown RustDesk package action")
	}
}
