package tailscale

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTailscale points the CLI at a script; it records each run in log.
func fakeTailscale(t *testing.T, script string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	path := filepath.Join(dir, "tailscale")
	body := "#!/bin/sh\necho \"$$ $*\" >> " + log + "\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := tailscaleBinary
	tailscaleBinary = path
	t.Cleanup(func() {
		cliMu.Lock()
		stopLoginLocked()
		cliMu.Unlock()
		tailscaleBinary = previous
	})
	return log
}

func alive(pid string) bool {
	data, err := os.ReadFile("/proc/" + pid + "/stat")
	// A killed child not yet reaped is a zombie: gone for our purpose.
	return err == nil && !strings.Contains(string(data), ") Z ")
}

func TestLoginLeavesOneProcess(t *testing.T) {
	log := fakeTailscale(t, `[ "$1" = login ] || exit 0; echo "To authenticate, visit: https://login.tailscale.com/a/$$" >&2; sleep 60`)
	cli := NewCli()
	var urls []string
	for range 3 {
		url, err := cli.Login()
		if err != nil {
			t.Fatal(err)
		}
		urls = append(urls, url)
	}
	if !strings.HasPrefix(urls[2], "https://login.tailscale.com/a/") {
		t.Fatalf("url %q", urls[2])
	}
	time.Sleep(200 * time.Millisecond)
	data, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d login runs: %q", len(lines), lines)
	}
	running := 0
	for _, line := range lines {
		if alive(strings.Fields(line)[0]) {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("%d login processes left running, want 1", running)
	}
	if err := cli.Logout(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if alive(strings.Fields(lines[2])[0]) {
		t.Fatal("logout left the login process running")
	}
}

func TestLoginWithoutAURLGivesUp(t *testing.T) {
	fakeTailscale(t, `sleep 60`)
	previous := loginURLWait
	loginURLWait = 300 * time.Millisecond
	defer func() { loginURLWait = previous }()
	start := time.Now()
	if _, err := NewCli().Login(); err == nil {
		t.Fatal("a login with no URL succeeded")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("login waited past its limit")
	}
	cliMu.Lock()
	defer cliMu.Unlock()
	if loginProcess != nil {
		t.Fatal("the login process was kept")
	}
}

func TestCommandsHaveALimitAndRunOneAtATime(t *testing.T) {
	log := fakeTailscale(t, `sleep 60`)
	previous := commandTimeout
	commandTimeout = 300 * time.Millisecond
	defer func() { commandTimeout = previous }()

	start := time.Now()
	if err := NewCli().Down(); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Fatalf("a hanging down: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("down waited past its limit")
	}

	_ = os.Remove(log)
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = NewCli().Down() }()
	}
	wg.Wait()
	// One at a time: each run was killed before the next started, so at
	// most one of them is ever alive.
	data, _ := os.ReadFile(log)
	if n := len(strings.Split(strings.TrimSpace(string(data)), "\n")); n != 3 {
		t.Fatalf("%d runs", n)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("three limited runs took %v: they ran side by side", elapsed)
	}
}

func TestUpWaitsForALoginAtMostItsOwnTimeout(t *testing.T) {
	log := fakeTailscale(t, `exit 0`)
	if err := NewCli().Up(); err != nil {
		t.Skipf("up did not run here: %v", err) // a running NetBird on the test host
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "up --accept-dns=false --timeout=") {
		t.Fatalf("up without its own timeout: %q", data)
	}
}
