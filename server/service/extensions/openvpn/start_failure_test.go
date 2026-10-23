package openvpn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailedPIDWriteDoesNotCreateAnotherRetryChain(t *testing.T) {
	f := newSupervisorFixture(t)
	f.s.startProfile = nil
	if err := os.WriteFile(f.s.path(supervisedID, ".ovpn"), []byte(strings.ReplaceAll(profile, "auth-user-pass\n", "")), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"openvpn", "resolvconf"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := os.Mkdir(f.s.runtime(supervisedID, ".pid"), 0700); err != nil {
		t.Fatal(err)
	}
	// A nonempty directory cannot be removed by startAttempt and cannot receive a rename.
	if err := os.WriteFile(filepath.Join(f.s.runtime(supervisedID, ".pid"), "keep"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	returned := make(chan struct{}, 1)
	restartSleep = func(time.Duration) { entered <- struct{}{}; <-release; returned <- struct{}{} }
	spawned := false
	defer func() {
		f.s.mu.Lock()
		f.s.nextGeneration(supervisedID)
		os.Remove(filepath.Join(f.s.dir, "active"))
		f.s.mu.Unlock()
		close(release)
		if spawned {
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Error("supervisor did not leave wait")
			}
		}
	}()
	// The caller already owns retries after a start error.
	if !f.s.restartOnce(supervisedID, 1, 1, f.s.startAttempt) {
		t.Fatal("PID write failure should remain retryable")
	}
	select {
	case <-entered:
		spawned = true
		t.Fatal("failed start spawned a second supervisor alongside the caller's retry chain")
	case <-time.After(time.Second):
	}
}
