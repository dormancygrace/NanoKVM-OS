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
	// NetBird and tailscaled are not running unless a test says otherwise.
	previous, previousRC, previousPidof := tailscaleBinary, rcServiceBinary, pidofBinary
	tailscaleBinary = path
	rcServiceBinary = fakeScript(t, dir, "rc-service", `echo "rc-service $*" >> `+log+`.rc; exit 1`)
	pidofBinary = fakeScript(t, dir, "pidof", `exit 1`)
	t.Cleanup(func() {
		cliMu.Lock()
		stopLoginLocked()
		cliMu.Unlock()
		tailscaleBinary, rcServiceBinary, pidofBinary = previous, previousRC, previousPidof
	})
	return log
}

func fakeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
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
	log := fakeTailscale(t, `sleep 60 & echo $! >> "$0.children"; wait`)
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
	// Its children went with it, as rc-service's do.
	children, _ := os.ReadFile(tailscaleBinary + ".children")
	time.Sleep(200 * time.Millisecond)
	for _, pid := range strings.Fields(string(children)) {
		if alive(pid) {
			t.Fatalf("child %s of a timed-out command still runs", pid)
		}
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
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "up --accept-dns=false --timeout=") {
		t.Fatalf("up without its own timeout: %q", data)
	}
}

// A probe that gives no answer is not "stopped": NetBird may still run, and
// tailscaled may still run when Uninstall would remove its packages.
func TestAProbeWithoutAnAnswerStopsTheAction(t *testing.T) {
	log := fakeTailscale(t, `exit 0`)
	previous := commandTimeout
	commandTimeout = 300 * time.Millisecond
	defer func() { commandTimeout = previous }()
	dir := filepath.Dir(tailscaleBinary)

	rcServiceBinary = fakeScript(t, dir, "rc-service-hang", `sleep 60`)
	if err := NewCli().Up(); err == nil || !strings.Contains(err.Error(), "NetBird") {
		t.Fatalf("up with a hanging NetBird check: %v", err)
	}
	if data, _ := os.ReadFile(log); strings.Contains(string(data), " up ") {
		t.Fatalf("tailscale up ran: %q", data)
	}
	if _, err := NewCli().Login(); err == nil {
		t.Fatal("login with a hanging NetBird check succeeded")
	}

	rcServiceBinary = fakeScript(t, dir, "rc-service-ok", `echo "rc-service $*" >> `+log+`.rc; exit 1`)
	pidofBinary = fakeScript(t, dir, "pidof-hang", `sleep 60`)
	if err := NewCli().Stop(); err == nil || !strings.Contains(err.Error(), "tailscaled") {
		t.Fatalf("stop with a hanging pidof: %v", err)
	}
	if err := NewCli().Start(); err == nil {
		t.Fatal("start with a hanging pidof succeeded")
	}

	// A normal "not running" answer still lets them through.
	pidofBinary = fakeScript(t, dir, "pidof-no", `exit 1`)
	if err := NewCli().Stop(); err != nil {
		t.Fatal(err)
	}
	if err := NewCli().Up(); err != nil {
		t.Fatal(err)
	}
}
