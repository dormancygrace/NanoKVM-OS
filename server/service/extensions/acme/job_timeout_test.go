package acme

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func installScript(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(binDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir(), "acme.sh"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// waitGone reports whether pid has gone (or is a zombie) within limit.
func waitGone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil || strings.Contains(string(stat), ") Z ") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// A timeout ends the whole acme.sh process tree. Its children (curl, a DNS
// hook) hold the output pipe; killing acme.sh alone left Wait blocked on
// them, and the job (and every later one) stuck for as long as they ran.
func TestTimeoutKillsTheProcessGroup(t *testing.T) {
	useTempTree(t)
	installScript(t, "sleep 60 &\nsleep 60\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runStep(ctx, &tailBuffer{}, nil, step{args: []string{"--cron"}}); err == nil {
		t.Fatal("a step past its deadline succeeded")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the step returned after %v", elapsed)
	}
}

// acme.sh can also exit by itself while something it started still runs.
// That must not outlive the step (and the job lock).
func TestEarlyExitEndsTheRestOfTheGroup(t *testing.T) {
	dir := useTempTree(t)
	pidFile := filepath.Join(dir, "child.pid")
	installScript(t, "sleep 60 >/dev/null 2>&1 &\necho $! > '"+pidFile+"'\nexit 0\n")
	if _, err := runStep(context.Background(), &tailBuffer{}, nil, step{args: []string{"--cron"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid <= 1 || !waitGone(pid, 2*time.Second) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("child %d of acme.sh still runs after the step", pid)
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatal(err)
	}
}

// Every entry point gets the DNS wait in its timeout: issue passes it as
// --dnssleep; renewals (manual and automatic) reload the saved one.
func TestDNSWaitExtendsTheJobTimeout(t *testing.T) {
	useTempTree(t)
	_ = os.MkdirAll(hooksDir(), 0o755)
	data, _ := os.ReadFile(filepath.Join("testdata", "dns_cf.sh"))
	_ = os.WriteFile(filepath.Join(hooksDir(), "dns_cf.sh"), data, 0o644)
	settings := Settings{Domains: []string{"*.example.org"}, CA: "letsencrypt", Challenge: "dns", DNSProvider: "dns_cf", DNSSleep: 3600}
	args, _, _, err := issueArgs(settings, IssueReq{DNSEnv: map[string]string{"CF_Token": "tok-123", "CF_Zone_ID": ""}})
	if err != nil {
		t.Fatal(err)
	}
	want := jobTimeout + time.Hour
	for name, steps := range map[string][]step{
		"issue":     {{args: args}, {args: installCertArgs(settings.primary())}},
		"renew":     {renewStep(&settings, true)},
		"automatic": {renewStep(&settings, false)},
	} {
		if got := stepsTimeout(steps); got != want {
			t.Errorf("%s: timeout %v, want %v", name, got, want)
		}
	}
	if got := stepsTimeout([]step{{args: []string{"--cron"}}}); got != jobTimeout {
		t.Fatalf("timeout without a DNS wait %v", got)
	}
}
