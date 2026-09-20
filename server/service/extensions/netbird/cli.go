package netbird

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
)

const (
	NetbirdPath      = "/usr/bin/netbird"
	netbirdSocket    = "unix:///var/run/netbird.sock"
	commandTimeout   = 45 * time.Second
	loginURLTimeout  = 60 * time.Second
	loginStopTimeout = 5 * time.Second
)

type Cli struct{}

type NbStatus struct {
	FQDN          string `json:"fqdn"`
	IP            string `json:"netbirdIp"`
	DaemonVersion string `json:"daemonVersion"`
	Management    struct {
		URL       string `json:"url"`
		Connected bool   `json:"connected"`
	} `json:"management"`
	Signal struct {
		Connected bool `json:"connected"`
	} `json:"signal"`
}

type loginProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

var activeLogin struct {
	sync.Mutex
	process *loginProcess
}

var loginURLRE = regexp.MustCompile(`https://[^[:space:]]+`)
var versionRE = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)*`)

func NewCli() *Cli { return &Cli{} }

func isInstalled() bool {
	info, err := os.Stat(NetbirdPath)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0
}

func (c *Cli) Start() error {
	if !isInstalled() {
		return errors.New("netbird is not installed")
	}
	if err := ensureProcessStopped("tailscaled", "Tailscale"); err != nil {
		return err
	}
	if err := runProgram(commandTimeout, "rc-update", "add", "netbird", "default"); err != nil {
		return err
	}
	running, err := c.ServiceRunning()
	if err != nil || running {
		return err
	}
	return runProgram(commandTimeout, "rc-service", "netbird", "start")
}

func (c *Cli) Restart() error {
	if !isInstalled() {
		return errors.New("netbird is not installed")
	}
	if err := ensureProcessStopped("tailscaled", "Tailscale"); err != nil {
		return err
	}
	if err := cancelLogin(); err != nil {
		return err
	}
	if err := runProgram(commandTimeout, "rc-update", "add", "netbird", "default"); err != nil {
		return err
	}
	running, err := c.ServiceRunning()
	if err != nil {
		return err
	}
	if !running {
		return runProgram(commandTimeout, "rc-service", "netbird", "start")
	}
	return runProgram(commandTimeout, "rc-service", "netbird", "restart")
}

func (c *Cli) Stop() error {
	if err := cancelLogin(); err != nil {
		return fmt.Errorf("cancel active NetBird login: %w", err)
	}
	running, err := c.ServiceRunning()
	if err != nil {
		return err
	}
	if running {
		if err = runProgram(commandTimeout, "rc-service", "netbird", "stop"); err != nil {
			return err
		}
	}
	_ = runProgram(commandTimeout, "rc-update", "del", "netbird", "default")
	return nil
}

func (c *Cli) Login() (string, error) {
	if err := ensureProcessStopped("tailscaled", "Tailscale"); err != nil {
		return "", err
	}
	running, err := c.ServiceRunning()
	if err != nil {
		return "", err
	}
	if !running {
		if err = c.Start(); err != nil {
			return "", err
		}
	}

	cmd := exec.Command(NetbirdPath, "up", "--daemon-addr", netbirdSocket, "--no-browser")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	process, err := startLogin(cmd)
	if err != nil {
		return "", err
	}

	urls := make(chan string, 1)
	var readers sync.WaitGroup
	readers.Add(2)
	go scanLoginURL(stdout, urls, &readers)
	go scanLoginURL(stderr, urls, &readers)
	result := make(chan error, 1)
	go func() {
		readers.Wait()
		err := cmd.Wait()
		finishLogin(process)
		result <- err
		close(process.done)
	}()

	select {
	case url := <-urls:
		return url, nil
	case err := <-result:
		if err == nil {
			return "", nil
		}
		return "", fmt.Errorf("netbird up failed: %w", err)
	case <-time.After(loginURLTimeout):
		_ = stopLogin(process)
		return "", errors.New("timed out waiting for NetBird login URL")
	}
}

func (c *Cli) Down() error {
	if err := cancelLogin(); err != nil {
		return err
	}
	return runProgram(commandTimeout, NetbirdPath, "down", "--daemon-addr", netbirdSocket)
}

func (c *Cli) Status() (*NbStatus, error) {
	output, err := runProgramOutput(commandTimeout, NetbirdPath, "status", "--json", "--daemon-addr", netbirdSocket)
	status, parseErr := parseStatus(output)
	if parseErr == nil {
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, parseErr
}

func (c *Cli) ServiceRunning() (bool, error) {
	if !isInstalled() {
		return false, nil
	}
	cmd := exec.Command("rc-service", "netbird", "status")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func parseStatus(output string) (*NbStatus, error) {
	if index := strings.Index(output, "{"); index >= 0 {
		output = output[index:]
	}
	var status NbStatus
	if strings.TrimSpace(output) == "" || json.Unmarshal([]byte(output), &status) != nil {
		return nil, errors.New("invalid NetBird status output")
	}
	return &status, nil
}

func installedVersion() string {
	output, _ := runProgramOutput(5*time.Second, NetbirdPath, "version")
	return versionRE.FindString(output)
}

func ensureProcessStopped(process, display string) error {
	if exec.Command("pidof", process).Run() == nil {
		return fmt.Errorf("stop %s before starting NetBird", display)
	}
	return nil
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

func startLogin(cmd *exec.Cmd) (*loginProcess, error) {
	activeLogin.Lock()
	defer activeLogin.Unlock()
	if activeLogin.process != nil {
		return nil, errors.New("NetBird login is already in progress")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &loginProcess{cmd: cmd, done: make(chan struct{})}
	activeLogin.process = process
	return process, nil
}

func finishLogin(process *loginProcess) {
	activeLogin.Lock()
	if activeLogin.process == process {
		activeLogin.process = nil
	}
	activeLogin.Unlock()
}

func cancelLogin() error {
	activeLogin.Lock()
	process := activeLogin.process
	activeLogin.Unlock()
	if process == nil {
		return nil
	}
	return stopLogin(process)
}

func stopLogin(process *loginProcess) error {
	err := syscall.Kill(-process.cmd.Process.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	select {
	case <-process.done:
		return nil
	case <-time.After(loginStopTimeout):
		return errors.New("NetBird login did not stop")
	}
}

func scanLoginURL(reader io.Reader, urls chan<- string, wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if url := loginURLRE.FindString(scanner.Text()); url != "" {
			select {
			case urls <- url:
			default:
			}
			return
		}
	}
}
