package wireguard

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// After a power cut the WAN can take minutes: restore keeps trying, with
// growing waits, until the profile is up, and stops when it is disabled.
func TestRestoreRetriesUntilTheTunnelIsUp(t *testing.T) {
	dir := t.TempDir()
	const id = "nk0123456789"
	if err := os.WriteFile(filepath.Join(dir, "active"), []byte(id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Service{dir: dir, failures: map[string]string{}}
	var waits []time.Duration
	attempts := 0
	up := func(string) error {
		attempts++
		if attempts < 7 {
			return errors.New("WireGuard up failed")
		}
		return nil
	}
	s.restoreWith(up, func(d time.Duration) { waits = append(waits, d) }, func(string) bool { return false })
	want := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute}
	if attempts != 7 || !reflect.DeepEqual(waits, want) {
		t.Fatalf("%d attempts, waits %v; want 7 and %v", attempts, waits, want)
	}
	if _, failed := s.failures[id]; failed {
		t.Fatal("failure kept after the tunnel came up")
	}

	// Disabled while waiting: no further attempt.
	attempts = 0
	disable := func(time.Duration) { _ = os.Remove(filepath.Join(dir, "active")) }
	if err := os.WriteFile(filepath.Join(dir, "active"), []byte(id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.restoreWith(func(string) error { attempts++; return errors.New("down") }, disable, func(string) bool { return false })
	if attempts != 1 {
		t.Fatalf("%d attempts after the profile was disabled, want 1", attempts)
	}
}
