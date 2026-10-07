package rustdesk

import (
	"bytes"
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

	"NanoKVM-Server/internal/apkrun"
	"NanoKVM-Server/service/vm"

	"golang.org/x/sys/unix"
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
	startedFile  string
	ensureUSB    func(bool) error

	// Package facts only change with the apk database, world or indexes, so
	// status polling reuses them until apkStamp changes or a package action runs.
	apkStamp func() string
	// apkMu keeps a repository refresh, including its apk state check, apart
	// from package actions; apkrun serializes the apk runs themselves with
	// everything else on the device.
	apkMu          sync.Mutex
	factsMu        sync.Mutex
	generation     int
	installed      installedFacts
	repository     repositoryFacts
	refreshing     chan struct{}
	failedStamp    string
	retryAt        time.Time
	repositoryWait time.Duration
}

type installedFacts struct {
	stamp     string
	installed bool
	version   string
}
type repositoryFacts struct {
	stamp     string
	version   string // installed version the upgrade candidate was selected for
	available bool
	update    string
}

// Loading the repository indexes takes apk several seconds of CPU and about
// 20 MiB on the device, so these queries run in the background, one at a time,
// at idle priority and with a deadline that lets them finish under load.
const repositoryTimeout = 3 * time.Minute
const repositoryRetry = time.Minute

type idlePriority struct{}

// Commands run with this context yield the CPU to requests and streaming.
func withIdlePriority(ctx context.Context) context.Context {
	return context.WithValue(ctx, idlePriority{}, true)
}

// Files apk rewrites whenever installed packages, repositories or their cached
// indexes change.
var apkStatePaths = []string{
	"/etc/apk/arch", "/etc/apk/keys", "/etc/apk/repositories", "/etc/apk/repositories.d",
	"/lib/apk/repositories.d", "/etc/apk/world", "/lib/apk/db/installed", "/etc/apk/cache", "/var/cache/apk",
}

func NewService(b *Bridge) *Service {
	s := &Service{bridge: b, run: runCommand, passwordFile: RuntimeDir + "/temporary-password", upstreamFile: "/usr/share/nanokvm-rustdesk/upstream.json", sourceFile: "/usr/share/nanokvm-rustdesk/source.json", startedFile: "/run/openrc/started/" + Package, ensureUSB: vm.EnsureRemoteAccessUSB,
		apkStamp: func() string { return apkStamp(apkStatePaths) }, repositoryWait: 250 * time.Millisecond}
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
	var data []byte
	var err error
	if name == "apk" {
		// The shared runner queues apk behind runs of other services and
		// processes within the context's deadline, and lowers the priority of
		// repository queries itself.
		data, err = apkrun.Command(ctx, name, args...).CombinedOutput()
	} else {
		cmd := exec.CommandContext(ctx, name, args...)
		var output bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &output
		err = cmd.Start()
		if err == nil {
			if ctx.Value(idlePriority{}) != nil {
				_ = unix.Setpriority(unix.PRIO_PROCESS, cmd.Process.Pid, 19)
			}
			err = cmd.Wait()
		}
		data = output.Bytes()
	}
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
	data, _, err := s.probe(context.Background(), 3*time.Second, name, args...)
	return data, err
}

// probe also reports whether the command finished before its deadline: an
// expired deadline says nothing about the package and is never cached.
func (s *Service) probe(parent context.Context, timeout time.Duration, name string, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	data, err := s.run(ctx, name, args...)
	return data, ctx.Err() == nil, err
}

// apkStamp summarizes the size and modification time of apk's state files and
// of the files in its state directories.
func apkStamp(paths []string) string {
	var b strings.Builder
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(&b, "%s:-;", path)
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
		if !info.IsDir() {
			continue
		}
		entries, _ := os.ReadDir(path)
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil {
				fmt.Fprintf(&b, "%s:%d:%d;", entry.Name(), info.Size(), info.ModTime().UnixNano())
			}
		}
	}
	return b.String()
}

// stamp identifies the apk state that cached facts were read from. Package
// actions also advance it in case file timestamps are too coarse to notice.
func (s *Service) stamp() string {
	s.factsMu.Lock()
	generation := s.generation
	s.factsMu.Unlock()
	return fmt.Sprintf("%d;%s", generation, s.apkStamp())
}
func (s *Service) forgetPackageFacts() {
	s.factsMu.Lock()
	s.generation++
	s.factsMu.Unlock()
}

func (s *Service) installedFacts(stamp string) installedFacts {
	s.factsMu.Lock()
	cached := s.installed
	s.factsMu.Unlock()
	if cached.stamp == stamp {
		return cached
	}
	facts := installedFacts{stamp: stamp}
	_, conclusive, err := s.probe(context.Background(), 3*time.Second, "apk", "info", "-e", Package)
	facts.installed = err == nil
	if facts.installed {
		version, finished, versionErr := s.probe(context.Background(), 3*time.Second, "apk", "info", "-e", "-v", Package)
		conclusive = conclusive && finished
		if versionErr == nil {
			value := strings.TrimSpace(string(version))
			if strings.HasPrefix(value, Package+"-") {
				facts.version = strings.TrimPrefix(value, Package+"-")
			}
		}
	}
	if conclusive {
		s.factsMu.Lock()
		s.installed = facts
		s.factsMu.Unlock()
	}
	return facts
}

// repositoryFacts starts a refresh after apk changed and only the request that
// starts it waits briefly. Until it finishes, the previous answer stands in;
// its upgrade candidate only counts for the installed version it was selected
// for. A refresh still running for an older state is followed by another one
// on a later request.
func (s *Service) repositoryFacts(stamp, version string) repositoryFacts {
	s.factsMu.Lock()
	defer s.factsMu.Unlock()
	if s.repository.stamp == stamp || s.refreshing != nil || s.failedStamp == stamp && time.Now().Before(s.retryAt) {
		return s.repository
	}
	done := make(chan struct{})
	s.refreshing = done
	go s.refreshRepository(stamp, version, done)
	s.factsMu.Unlock()
	timer := time.NewTimer(s.repositoryWait)
	select {
	case <-done:
	case <-timer.C:
	}
	timer.Stop()
	s.factsMu.Lock()
	return s.repository
}
func (s *Service) refreshRepository(stamp, version string, done chan struct{}) {
	defer close(done)
	s.apkMu.Lock()
	var facts repositoryFacts
	var err error
	// A package action may have changed apk while this waited; the next
	// status request then asks again.
	current := s.stamp() == stamp
	if current {
		facts, err = s.queryRepository(version)
	}
	s.apkMu.Unlock()
	s.factsMu.Lock()
	defer s.factsMu.Unlock()
	s.refreshing = nil
	if !current {
		return
	}
	// A failed query reports nothing, as before, and is retried later.
	if err != nil {
		s.failedStamp, s.retryAt = stamp, time.Now().Add(repositoryRetry)
		stamp = ""
	}
	facts.stamp = stamp
	s.repository = facts
}
func (s *Service) queryRepository(version string) (repositoryFacts, error) {
	facts := repositoryFacts{version: version}
	var err, updateErr error
	_, facts.available, err = s.repositoryVersion(false)
	if version != "" {
		facts.update, _, updateErr = s.repositoryVersion(true)
	}
	return facts, errors.Join(err, updateErr)
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

// packageCommand holds the index lock so that a status query never loads the
// indexes alongside an apk transaction.
func (s *Service) packageCommand(args ...string) error {
	s.apkMu.Lock()
	defer s.apkMu.Unlock()
	defer s.forgetPackageFacts()
	_, err := s.command("apk", args...)
	return err
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
	stamp := s.stamp()
	installed := s.installedFacts(stamp)
	status.Installed = installed.installed
	// Status polling reads the existing indexes; repository refresh belongs to package management.
	repository := s.repositoryFacts(stamp, installed.version)
	status.Available = repository.available
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
		status.Version = installed.version
		if status.Version != "" && repository.version == status.Version {
			status.UpdateVersion = repository.update
		}
		status.Running = s.daemonRunning()
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
// Only a failed query is an error; a missing or unreadable answer is not.
func (s *Service) repositoryVersion(upgradable bool) (string, bool, error) {
	args := []string{"query", "--no-network"}
	if !upgradable {
		args = append(args, "--from=repositories")
	}
	// Upgrade selection needs the installed database as well as the repositories.
	args = append(args, "--format=json", "--fields=name,version")
	if upgradable {
		args = append(args, "--upgradable")
	}
	data, _, err := s.probe(withIdlePriority(context.Background()), repositoryTimeout, "apk", append(args, Package)...)
	if err != nil {
		return "", false, err
	}
	var packages []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &packages) != nil {
		return "", false, nil
	}
	for _, p := range packages {
		if p.Name == Package && p.Version != "" {
			return p.Version, true, nil
		}
	}
	return "", false, nil
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
		return s.packageCommand("add", Package)
	case "upgrade":
		return s.packageCommand("upgrade", Package)
	case "remove":
		if err := s.packageCommand("del", Package); err != nil {
			return err
		}
		s.bridge.Stop()
		return nil
	default:
		return errors.New("unknown RustDesk package action")
	}
}
