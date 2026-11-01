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

const supervisedID = "ov0123456789"

// supervisorFixture is an enabled profile whose starts are recorded by a
// fake: start returns the next error of results, then nil.
type supervisorFixture struct {
	s       *Service
	slept   []time.Duration
	starts  []int
	results []error
	onSleep func()
}

func newSupervisorFixture(t *testing.T) *supervisorFixture {
	t.Helper()
	previous := restartSleep
	t.Cleanup(func() { restartSleep = previous })
	f := &supervisorFixture{s: &Service{dir: t.TempDir(), run: t.TempDir()}}
	restartSleep = func(d time.Duration) {
		f.slept = append(f.slept, d)
		if f.onSleep != nil {
			f.onSleep()
		}
	}
	f.s.startProfile = func(p Profile, attempt int, generation uint64) error {
		if p.ID != supervisedID {
			t.Fatalf("started %s", p.ID)
		}
		f.starts = append(f.starts, attempt)
		if len(f.results) > 0 {
			err := f.results[0]
			f.results = f.results[1:]
			return err
		}
		return nil
	}
	if err := f.s.save([]Profile{{ID: supervisedID, Name: "office"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.s.dir, "active"), []byte(supervisedID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if f.s.desired() != supervisedID {
		t.Fatal("fixture profile is not the enabled one")
	}
	f.s.generation = map[string]uint64{supervisedID: 1}
	return f
}

// Wrong credentials and a deleted profile are terminal.
func TestSuperviseStopsForTerminalConditions(t *testing.T) {
	f := newSupervisorFixture(t)
	if err := os.WriteFile(f.s.runtime(supervisedID, ".log"), []byte("AUTH: Received control message: AUTH_FAILED\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.s.supervise(supervisedID, 1, 0, time.Second)
	if len(f.starts) != 0 {
		t.Fatalf("restarted after AUTH_FAILED: %v", f.starts)
	}

	f = newSupervisorFixture(t)
	if err := f.s.save(nil); err != nil {
		t.Fatal(err)
	}
	f.s.supervise(supervisedID, 1, 0, time.Second)
	if len(f.starts) != 0 {
		t.Fatalf("started a deleted profile: %v", f.starts)
	}

	f = newSupervisorFixture(t)
	if err := f.s.save([]Profile{{ID: supervisedID, NeedsAuth: true}}); err != nil {
		t.Fatal(err)
	}
	f.s.supervise(supervisedID, 1, 0, time.Second)
	if len(f.starts) != 0 {
		t.Fatalf("started without stored credentials: %v", f.starts)
	}
}
