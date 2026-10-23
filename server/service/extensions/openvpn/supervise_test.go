package openvpn

import (
	"errors"
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

// A start that fails before OpenVPN runs is retried with the backoff until
// it succeeds; then the started OpenVPN's own supervisor takes over.
func TestSuperviseRetriesFailedStarts(t *testing.T) {
	f := newSupervisorFixture(t)
	f.results = []error{errors.New("resolvconf failed"), errors.New("resolvconf failed")}
	f.s.supervise(supervisedID, 1, 0, time.Second)
	if !reflect.DeepEqual(f.starts, []int{1, 2, 3}) {
		t.Fatalf("start attempts %v, want 1 2 3", f.starts)
	}
	if want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}; !reflect.DeepEqual(f.slept, want) {
		t.Fatalf("waits %v, want %v", f.slept, want)
	}
}

// A tunnel that ran for a while starts its backoff from the first delay.
func TestSuperviseResetsTheBackoffAfterALongRun(t *testing.T) {
	f := newSupervisorFixture(t)
	f.s.supervise(supervisedID, 1, 4, time.Hour)
	if !reflect.DeepEqual(f.starts, []int{1}) || !reflect.DeepEqual(f.slept, []time.Duration{5 * time.Second}) {
		t.Fatalf("starts %v waits %v", f.starts, f.slept)
	}
}

// Disabled while waiting: no start, and the loop ends even though the last
// start failed.
func TestSuperviseStopsWhenDisabled(t *testing.T) {
	f := newSupervisorFixture(t)
	f.results = []error{errors.New("resolvconf failed")}
	f.onSleep = func() {
		if len(f.starts) == 1 {
			_ = os.Remove(filepath.Join(f.s.dir, "active"))
		}
	}
	f.s.supervise(supervisedID, 1, 0, time.Second)
	if !reflect.DeepEqual(f.starts, []int{1}) {
		t.Fatalf("start attempts %v after disabling", f.starts)
	}
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

// A manual start (or a stop) begins a new retry chain: a timer of the older
// chain that fires afterwards must not start another child, so one backoff
// governs the profile.
func TestStaleSupervisorStartsNothing(t *testing.T) {
	f := newSupervisorFixture(t)
	release := map[uint64]chan struct{}{1: make(chan struct{}), 2: make(chan struct{})}
	sleeping := make(chan uint64, 2)
	var current uint64
	restartSleep = func(time.Duration) {
		g := current
		sleeping <- g
		<-release[g]
	}
	done := make(chan struct{}, 2)
	current = 1
	go func() { f.s.supervise(supervisedID, 1, 0, time.Second); done <- struct{}{} }()
	<-sleeping
	// The manual start of Change("up") while chain 1 waits.
	f.s.mu.Lock()
	f.s.nextGeneration(supervisedID)
	f.s.mu.Unlock()
	current = 2
	go func() { f.s.supervise(supervisedID, 2, 0, time.Second); done <- struct{}{} }()
	<-sleeping
	close(release[1])
	<-done
	if len(f.starts) != 0 {
		t.Fatalf("the stale chain started a child: %v", f.starts)
	}
	close(release[2])
	<-done
	if len(f.starts) != 1 {
		t.Fatalf("the current chain made %d starts, want 1", len(f.starts))
	}
}

// Disabling ends a pending chain through stop as well.
func TestStopEndsThePendingChain(t *testing.T) {
	f := newSupervisorFixture(t)
	f.onSleep = func() {
		f.s.mu.Lock()
		f.s.nextGeneration(supervisedID) // what stop() does first
		f.s.mu.Unlock()
	}
	f.s.supervise(supervisedID, 1, 0, time.Second)
	if len(f.starts) != 0 {
		t.Fatalf("started after a stop: %v", f.starts)
	}
}
