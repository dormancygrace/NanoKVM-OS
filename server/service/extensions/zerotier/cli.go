package zerotier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/internal/toolcache"
	"NanoKVM-Server/proto"
)

const (
	// The nanokvm-zerotier package keeps ZeroTier's own names and home, so an
	// identity made by another installation carries over.
	OnePath        = "/usr/sbin/zerotier-one"
	CliPath        = "/usr/sbin/zerotier-cli"
	serviceName    = "zerotier-one"
	commandTimeout = 30 * time.Second
)

// A network ID is 16 hexadecimal digits: the controller's address and a
// network number. Nothing else reaches zerotier-cli.
var networkID = regexp.MustCompile(`^[0-9a-f]{16}$`)

var versionRE = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

type Cli struct{}

func NewCli() *Cli { return &Cli{} }

// ValidNetworkID normalizes a network ID from the page and reports whether
// it is one.
func ValidNetworkID(value string) (string, bool) {
	id := strings.ToLower(strings.TrimSpace(value))
	return id, networkID.MatchString(id)
}

func isInstalled() bool {
	info, err := os.Stat(OnePath)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0
}

func (c *Cli) Start() error {
	if !isInstalled() {
		return errors.New("ZeroTier is not installed")
	}
	if err := runProgram(commandTimeout, "rc-update", "add", serviceName, "default"); err != nil {
		return err
	}
	running, err := c.ServiceRunning()
	if err != nil || running {
		return err
	}
	return startDaemon("start")
}

func (c *Cli) Restart() error {
	if !isInstalled() {
		return errors.New("ZeroTier is not installed")
	}
	if err := runProgram(commandTimeout, "rc-update", "add", serviceName, "default"); err != nil {
		return err
	}
	running, err := c.ServiceRunning()
	if err != nil {
		return err
	}
	if !running {
		return startDaemon("start")
	}
	return startDaemon("restart")
}

func (c *Cli) Stop() error {
	running, err := c.ServiceRunning()
	if err != nil {
		return err
	}
	if running {
		if err = runProgram(commandTimeout, "rc-service", serviceName, "stop"); err != nil {
			return err
		}
	}
	_ = runProgram(commandTimeout, "rc-update", "del", serviceName, "default")
	return nil
}

func (c *Cli) ServiceRunning() (bool, error) {
	if !isInstalled() || !openrcMayRun(serviceName) {
		return false, nil
	}
	cmd := exec.Command("rc-service", serviceName, "status")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Join and Leave take a network ID checked by ValidNetworkID. Joining only
// asks the network's controller; an administrator may still have to
// authorize the device there.
func (c *Cli) Join(id string) error {
	if _, ok := ValidNetworkID(id); !ok {
		return errors.New("invalid ZeroTier network ID")
	}
	return runProgram(commandTimeout, CliPath, "join", id)
}

func (c *Cli) Leave(id string) error {
	if _, ok := ValidNetworkID(id); !ok {
		return errors.New("invalid ZeroTier network ID")
	}
	return runProgram(commandTimeout, CliPath, "leave", id)
}

type nodeInfo struct {
	Address string `json:"address"`
	Online  bool   `json:"online"`
	Version string `json:"version"`
}

type networkInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Type      string   `json:"type"`
	Device    string   `json:"portDeviceName"`
	Addresses []string `json:"assignedAddresses"`
}

func (c *Cli) Info() (*nodeInfo, error) {
	output, err := runProgramJSON(commandTimeout, CliPath, "-j", "info")
	if err != nil {
		return nil, err
	}
	return parseInfo(output)
}

func (c *Cli) Networks() ([]proto.ZerotierNetwork, error) {
	output, err := runProgramJSON(commandTimeout, CliPath, "-j", "listnetworks")
	if err != nil {
		return nil, err
	}
	return parseNetworks(output)
}

func parseInfo(output string) (*nodeInfo, error) {
	var info nodeInfo
	if json.Unmarshal([]byte(output), &info) != nil || info.Address == "" {
		return nil, errors.New("invalid ZeroTier status output")
	}
	return &info, nil
}

func parseNetworks(output string) ([]proto.ZerotierNetwork, error) {
	var list []networkInfo
	if json.Unmarshal([]byte(output), &list) != nil {
		return nil, errors.New("invalid ZeroTier network list")
	}
	networks := make([]proto.ZerotierNetwork, 0, len(list))
	for _, n := range list {
		addresses := n.Addresses
		if addresses == nil {
			addresses = []string{}
		}
		networks = append(networks, proto.ZerotierNetwork{
			ID: n.ID, Name: n.Name, Status: n.Status, Type: n.Type, Device: n.Device, Addresses: addresses,
		})
	}
	return networks, nil
}

// firstIPv4 is the address the VPN list shows: the first IPv4 address of the
// first network that is up.
func firstIPv4(networks []proto.ZerotierNetwork) string {
	for _, n := range networks {
		if n.Status != "OK" {
			continue
		}
		for _, value := range n.Addresses {
			if ip, _, err := net.ParseCIDR(value); err == nil && ip.To4() != nil {
				return ip.String()
			}
		}
	}
	return ""
}

var openrcStartedDir = "/run/openrc/started"

// openrcMayRun is false for a service without its started link, as for
// NetBird: rc-service status need not run for it.
func openrcMayRun(service string) bool {
	if _, err := os.Lstat(openrcStartedDir); err != nil {
		return true
	}
	_, err := os.Lstat(filepath.Join(openrcStartedDir, service))
	return !errors.Is(err, os.ErrNotExist)
}

// A stopped service reports this version on every status poll; it only
// changes when the program is replaced.
var versions = toolcache.New(func(path string, args ...string) (string, error) {
	return runProgramOutput(5*time.Second, path, args...)
}, time.Minute)

func installedVersion() string {
	output, _ := versions.Output(OnePath, "-v")
	return versionRE.FindString(output)
}

func runProgram(timeout time.Duration, name string, args ...string) error {
	_, err := runProgramOutput(timeout, name, args...)
	return err
}

func runProgramOutput(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if ctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("%s timed out", name)
	}
	if err != nil && text != "" {
		return text, fmt.Errorf("%w: %s", err, text)
	}
	return text, err
}

// runProgramJSON is runProgramOutput for a JSON answer: stdout only, so that
// a warning on stderr cannot break the parse; stderr explains a failure.
func runProgramJSON(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s timed out", name)
	}
	if err != nil {
		if text := strings.TrimSpace(stderr.String() + "\n" + string(output)); text != "" {
			return "", fmt.Errorf("%w: %s", err, text)
		}
		return "", err
	}
	return string(output), nil
}

// startDaemon starts the service through OpenRC without the server's OOM
// protection, which it would otherwise inherit.
func startDaemon(action string) error {
	argv := oomscore.Unprotected("rc-service", serviceName, action)
	return runProgram(commandTimeout, argv[0], argv[1:]...)
}
