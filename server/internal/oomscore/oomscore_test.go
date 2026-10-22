package oomscore

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestSetAndGetChild(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	own, err := get(0)
	if err != nil {
		t.Skip(err)
	}
	if got, err := get(cmd.Process.Pid); err != nil || got != own {
		t.Fatalf("child inherited %d %v, want %d", got, err, own)
	}
	// Raising needs no privilege.
	if err = Set(cmd.Process.Pid, Expendable); err != nil {
		t.Fatal(err)
	}
	if got, err := get(cmd.Process.Pid); err != nil || got != Expendable {
		t.Fatalf("got %d %v", got, err)
	}
}

func TestUnprotectedResetsBeforeExec(t *testing.T) {
	// A child stands in for the protected server: it raises its own value,
	// which needs no privilege, then starts the program through Unprotected.
	argv := Unprotected("cat", "/proc/self/oom_score_adj")
	script := "echo 500 >/proc/self/oom_score_adj && exec \"$@\""
	out, err := exec.Command("/bin/sh", append([]string{"-c", script, "sh"}, argv...)...).Output()
	if err != nil {
		t.Skip(err)
	}
	if got := string(out); got != "0\n" {
		t.Fatalf("program ran with oom_score_adj %q, want 0", got)
	}
}

// get reads the adjustment for pid; pid 0 means the calling process.
func get(pid int) (int, error) {
	target := "self"
	if pid != 0 {
		target = strconv.Itoa(pid)
	}
	data, err := os.ReadFile(procRoot + "/" + target + "/oom_score_adj")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func TestUnprotectedResetsInheritedNice(t *testing.T) {
	argv := Unprotected("sh", "-c", `ps -o ni= -p $$`)
	// Only the short-lived child gets boosted. Ordinary host tests skip when
	// CAP_SYS_NICE is unavailable; this also runs in the privileged host check.
	script := `[ "$(ps -o ni= -p $$ | tr -d ' ')" = -10 ] || exit 77; exec "$@"`
	args := append([]string{"-n", "-10", "/bin/sh", "-c", script, "sh"}, argv...)
	out, err := exec.Command("nice", args...).Output()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
		t.Skip("CAP_SYS_NICE unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "0" {
		t.Fatalf("daemon inherited nice %q, want 0", got)
	}
}
