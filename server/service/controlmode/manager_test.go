package controlmode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSwitchPreemptsBeforeWaitingForActiveWrite(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "mode"), ModeMCP)
	release, err := manager.AcquireWrite(ModeMCP)
	if err != nil {
		t.Fatal(err)
	}

	preempted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manager.SwitchWithCleanup(ModePicoclaw, func() error {
			close(preempted)
			return nil
		}, nil)
	}()

	select {
	case <-preempted:
	case <-time.After(time.Second):
		t.Fatal("preempt callback was not called before waiting")
	}
	if _, err := manager.AcquireWrite(ModeMCP); err == nil {
		t.Fatal("new write acquired control during transition")
	}

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("switch did not resume after active write was released")
	}
	if got := manager.Current(); got != ModePicoclaw {
		t.Fatalf("mode = %q, want picoclaw", got)
	}
}

func TestSwitchCleanupFailureFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	manager := NewManager(path, ModePicoclaw)
	wantErr := errors.New("hid release failed")

	if err := manager.SwitchWithCleanup(ModeMCP, nil, func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if got := manager.Current(); got != ModeOff {
		t.Fatalf("mode = %q, want off after cleanup failure", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "off\n" {
		t.Fatalf("mode file = %q, want off", data)
	}
}

func TestInvalidModeFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(path, ModePicoclaw)
	status, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Mode != ModeOff {
		t.Fatalf("mode = %q, want off", status.Mode)
	}
	if !strings.Contains(status.LastError, "invalid") {
		t.Fatalf("last_error = %q, want invalid mode detail", status.LastError)
	}
}
