package openvpn

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRestartDelayGrowsToFiveMinutes(t *testing.T) {
	var got []time.Duration
	for n := 1; n <= 8; n++ {
		got = append(got, restartDelay(n))
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second,
		80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delays %v, want %v", got, want)
	}
}

// A tunnel the user disabled is not restarted, and a tunnel that ran for a
// while starts its backoff again from the first delay.
func TestSuperviseRestartsOnlyTheEnabledProfile(t *testing.T) {
	previous := restartSleep
	defer func() { restartSleep = previous }()
	var slept []time.Duration
	restartSleep = func(d time.Duration) { slept = append(slept, d) }

	s := &Service{dir: t.TempDir(), run: t.TempDir()}
	s.supervise("nv0123456789", 3, time.Second) // not enabled: no start
	s.supervise("nv0123456789", 3, time.Hour)   // ran long: backoff resets
	if want := []time.Duration{40 * time.Second, 5 * time.Second}; !reflect.DeepEqual(slept, want) {
		t.Fatalf("waits %v, want %v", slept, want)
	}
	// Enabled, but its profile is gone: nothing to start, no panic.
	if err := os.WriteFile(filepath.Join(s.dir, "active"), []byte("nv0123456789\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.supervise("nv0123456789", 0, time.Second)

	// Wrong credentials: no restart, even with the profile present.
	if err := s.save([]Profile{{ID: "nv0123456789", Name: "office"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.runtime("nv0123456789", ".log"), []byte("AUTH: Received control message: AUTH_FAILED\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.supervise("nv0123456789", 0, time.Second)
	if _, err := os.Stat(s.runtime("nv0123456789", ".pid")); !os.IsNotExist(err) {
		t.Fatalf("restarted after AUTH_FAILED: %v", err)
	}
}
