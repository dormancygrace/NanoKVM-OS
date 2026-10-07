package oomscore

import (
	"os/exec"
	"testing"
)

func TestSetAndGetChild(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	own, err := Get(0)
	if err != nil {
		t.Skip(err)
	}
	if got, err := Get(cmd.Process.Pid); err != nil || got != own {
		t.Fatalf("child inherited %d %v, want %d", got, err, own)
	}
	// Raising needs no privilege.
	if err = Set(cmd.Process.Pid, Expendable); err != nil {
		t.Fatal(err)
	}
	if got, err := Get(cmd.Process.Pid); err != nil || got != Expendable {
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
