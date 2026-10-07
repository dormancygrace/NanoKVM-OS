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
