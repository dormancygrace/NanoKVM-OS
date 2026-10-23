package tailscale

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/internal/oomscore"
)

const TailscaledPath = "/usr/sbin/tailscaled"

type Cli struct{}

type TsStatus struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		HostName     string   `json:"HostName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	CurrentTailnet struct {
		Name string `json:"Name"`
	} `json:"CurrentTailnet"`
}

func NewCli() *Cli { return &Cli{} }

// The CLI and rc-service calls had no time limit and ran side by side: a
// "tailscale up" waiting for a login, or a slow tailscaled, held the request
// and its Go process for ever, and every Login click left another
// "tailscale login --timeout=10m" behind. Commands that change state now
// run one at a time, each with a limit, and one login process at most.
var (
	tailscaleBinary = TailscalePath
	rcServiceBinary = "rc-service"
	pidofBinary     = "pidof"
	cliMu           sync.Mutex
	loginProcess    *exec.Cmd // under cliMu

	serviceTimeout = 60 * time.Second // rc-service start/stop/restart
	commandTimeout = 30 * time.Second // up, down, logout, status
	loginURLWait   = 30 * time.Second // until login prints its URL
)

// upWait is how long "tailscale up" waits for tailscaled to run; without it
// up blocks for ever on a node that needs a login.
const upWait = "--timeout=20s"

func command(timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// rc-service runs openrc-run and start-stop-daemon below it: end the
	// whole group at the limit, not only rc-service. tailscaled is
	// daemonized into a session of its own and is not in it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("%s %s: %w in %v", name, strings.Join(args, " "), errNoAnswer, timeout)
	}
	return err
}

var errNoAnswer = errors.New("no answer")

// probe runs a status check: true when it exits 0, false when it exits with
// a status (not running), and an error when it gave no answer or could not
// run at all. A probe that timed out says nothing about the state.
func probe(name string, args ...string) (bool, error) {
	err := command(commandTimeout, name, args...)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return false, err
}

func tailscaledRunning() (bool, error) {
	running, err := probe(pidofBinary, "tailscaled")
	if err != nil {
		return false, fmt.Errorf("cannot tell whether tailscaled runs: %w", err)
	}
	return running, nil
}

// stopLoginLocked ends a login a previous click left waiting.
func stopLoginLocked() {
	if loginProcess != nil && loginProcess.Process != nil {
		_ = loginProcess.Process.Kill()
	}
	loginProcess = nil
}

func (c *Cli) Start() error {
	cliMu.Lock()
	defer cliMu.Unlock()
	if !isInstalled() {
		return errors.New("Tailscale is not installed")
	}
	if err := ensureNetbirdStopped(); err != nil {
		return err
	}
	if err := command(commandTimeout, "rc-update", "add", "tailscale", "default"); err != nil {
		return err
	}
	running, err := tailscaledRunning()
	if err != nil || running {
		return err
	}
	return startDaemon("start")
}

func (c *Cli) Restart() error {
	cliMu.Lock()
	defer cliMu.Unlock()
	if !isInstalled() {
		return errors.New("Tailscale is not installed")
	}
	if err := ensureNetbirdStopped(); err != nil {
		return err
	}
	if err := command(commandTimeout, "rc-update", "add", "tailscale", "default"); err != nil {
		return err
	}
	running, err := tailscaledRunning()
	if err != nil {
		return err
	}
	if !running {
		return startDaemon("start")
	}
	return startDaemon("restart")
}

func (c *Cli) Stop() error {
	cliMu.Lock()
	defer cliMu.Unlock()
	stopLoginLocked()
	// Uninstall removes the packages once Stop succeeds: a probe that gave
	// no answer must not pass for "not running".
	running, err := tailscaledRunning()
	if err != nil {
		return err
	}
	if running {
		if err := command(serviceTimeout, rcServiceBinary, "tailscale", "stop"); err != nil {
			return err
		}
	}
	_ = command(commandTimeout, "rc-update", "del", "tailscale", "default")
	return nil
}

func (c *Cli) Up() error {
	cliMu.Lock()
	defer cliMu.Unlock()
	if err := ensureNetbirdStopped(); err != nil {
		return err
	}
	return command(commandTimeout, tailscaleBinary, "up", "--accept-dns=false", upWait)
}

func (c *Cli) Down() error {
	cliMu.Lock()
	defer cliMu.Unlock()
	return command(commandTimeout, tailscaleBinary, "down")
}

// Status is polled every 5 s by the VPN page. It asks tailscaled directly
// and starts the client only when the local API gives no usable answer.
func (c *Cli) Status() (*TsStatus, error) {
	status, err := localStatus(localAPISocket)
	if err == nil || errors.Is(err, errDaemonUnreachable) {
		return status, err
	}
	return cliStatus()
}

var cliStatus = func() (*TsStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tailscaleBinary, "status", "--json")
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	text := string(output)
	if index := strings.Index(text, "{"); index >= 0 {
		output = []byte(text[index:])
	} else {
		return nil, errors.New("unknown output")
	}
	var status TsStatus
	if err = json.Unmarshal(output, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// Login starts "tailscale login" and returns the URL it prints. The process
// stays to finish the login (up to 10 minutes); a new Login, Logout or Stop
// ends it, so clicks do not pile up processes.
func (c *Cli) Login() (string, error) {
	cliMu.Lock()
	defer cliMu.Unlock()
	if err := ensureNetbirdStopped(); err != nil {
		return "", err
	}
	stopLoginLocked()
	cmd := exec.Command(tailscaleBinary, "login", "--accept-dns=false", "--timeout=10m")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	loginProcess = cmd
	go func() {
		_ = cmd.Wait()
		cliMu.Lock()
		if loginProcess == cmd {
			loginProcess = nil
		}
		cliMu.Unlock()
	}()

	type result struct {
		url string
		err error
	}
	found := make(chan result, 1)
	go func() {
		reader := bufio.NewReader(stderr)
		urlPattern := regexp.MustCompile(`https://[^[:space:]]+`)
		for {
			line, readErr := reader.ReadString('\n')
			if url := urlPattern.FindString(line); url != "" {
				found <- result{url: url}
				// Keep draining so the process never blocks on a full pipe.
				_, _ = io.Copy(io.Discard, reader)
				return
			}
			if readErr != nil {
				found <- result{err: readErr}
				return
			}
		}
	}()
	select {
	case r := <-found:
		if r.err != nil {
			stopLoginLocked()
		}
		return r.url, r.err
	case <-time.After(loginURLWait):
		stopLoginLocked()
		return "", fmt.Errorf("tailscale login printed no URL in %v", loginURLWait)
	}
}

func (c *Cli) Logout() error {
	cliMu.Lock()
	defer cliMu.Unlock()
	stopLoginLocked()
	return command(commandTimeout, tailscaleBinary, "logout")
}

// IsRunning is true only when tailscaled is known to run.
func (c *Cli) IsRunning() bool {
	running, err := tailscaledRunning()
	return err == nil && running
}

func isInstalled() bool {
	client, clientErr := os.Stat(TailscalePath)
	daemon, daemonErr := os.Stat(TailscaledPath)
	return clientErr == nil && daemonErr == nil && client.Mode().IsRegular() && daemon.Mode().IsRegular() && client.Mode()&0111 != 0 && daemon.Mode()&0111 != 0
}

// ensureNetbirdStopped lets Tailscale start only when NetBird is known to be
// stopped: rc-service answered with "not running". No answer is not that.
func ensureNetbirdStopped() error {
	running, err := probe(rcServiceBinary, "netbird", "status")
	if err != nil {
		return fmt.Errorf("cannot tell whether NetBird runs: %w", err)
	}
	if running {
		return fmt.Errorf("stop NetBird before starting Tailscale")
	}
	return nil
}

// startDaemon starts tailscaled through OpenRC without the server's OOM
// protection, which it would otherwise inherit.
func startDaemon(action string) error {
	argv := oomscore.Unprotected("rc-service", "tailscale", action)
	return command(serviceTimeout, argv[0], argv[1:]...)
}
