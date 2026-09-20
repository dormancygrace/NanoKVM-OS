package tailscale

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
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

func (c *Cli) Start() error {
	if !isInstalled() {
		return errors.New("Tailscale is not installed")
	}
	if err := ensureNetbirdStopped(); err != nil {
		return err
	}
	if err := exec.Command("rc-update", "add", "tailscale", "default").Run(); err != nil {
		return err
	}
	if c.IsRunning() {
		return nil
	}
	return exec.Command("rc-service", "tailscale", "start").Run()
}

func (c *Cli) Restart() error {
	if !isInstalled() {
		return errors.New("Tailscale is not installed")
	}
	if err := ensureNetbirdStopped(); err != nil {
		return err
	}
	if err := exec.Command("rc-update", "add", "tailscale", "default").Run(); err != nil {
		return err
	}
	if !c.IsRunning() {
		return exec.Command("rc-service", "tailscale", "start").Run()
	}
	return exec.Command("rc-service", "tailscale", "restart").Run()
}

func (c *Cli) Stop() error {
	if c.IsRunning() {
		if err := exec.Command("rc-service", "tailscale", "stop").Run(); err != nil {
			return err
		}
	}
	_ = exec.Command("rc-update", "del", "tailscale", "default").Run()
	return nil
}

func (c *Cli) Up() error {
	if err := ensureNetbirdStopped(); err != nil {
		return err
	}
	return exec.Command(TailscalePath, "up", "--accept-dns=false").Run()
}

func (c *Cli) Down() error { return exec.Command(TailscalePath, "down").Run() }

func (c *Cli) Status() (*TsStatus, error) {
	output, err := exec.Command(TailscalePath, "status", "--json").CombinedOutput()
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

func (c *Cli) Login() (string, error) {
	if err := ensureNetbirdStopped(); err != nil {
		return "", err
	}
	cmd := exec.Command(TailscalePath, "login", "--accept-dns=false", "--timeout=10m")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	go func() { _ = cmd.Wait() }()
	reader := bufio.NewReader(stderr)
	urlPattern := regexp.MustCompile(`https://[^[:space:]]+`)
	for {
		line, readErr := reader.ReadString('\n')
		if url := urlPattern.FindString(line); url != "" {
			return url, nil
		}
		if readErr != nil {
			return "", readErr
		}
	}
}

func (c *Cli) Logout() error   { return exec.Command(TailscalePath, "logout").Run() }
func (c *Cli) IsRunning() bool { return exec.Command("pidof", "tailscaled").Run() == nil }

func isInstalled() bool {
	client, clientErr := os.Stat(TailscalePath)
	daemon, daemonErr := os.Stat(TailscaledPath)
	return clientErr == nil && daemonErr == nil && client.Mode().IsRegular() && daemon.Mode().IsRegular() && client.Mode()&0111 != 0 && daemon.Mode()&0111 != 0
}

func ensureNetbirdStopped() error {
	if exec.Command("rc-service", "netbird", "status").Run() == nil {
		return fmt.Errorf("stop NetBird before starting Tailscale")
	}
	return nil
}
