package startpriority

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type recorder struct {
	mu    sync.Mutex
	value map[int]int
	fail  error
}

func (r *recorder) set(tid, value int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.value[tid] = value
	return nil
}

func (r *recorder) get(tid int) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.value[tid]
	return v, ok
}

func setup(t *testing.T, tids ...string) *recorder {
	t.Helper()
	dir := t.TempDir()
	for _, tid := range tids {
		if err := os.Mkdir(filepath.Join(dir, tid), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rec := &recorder{value: map[int]int{}}
	oldDir, oldSet, oldAfter := taskDir, setPriority, AfterViewer
	taskDir, setPriority, AfterViewer = dir, rec.set, 100*time.Millisecond
	mu.Lock()
	raised, released, timer = false, false, nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		raised, released, timer = false, false, nil
		mu.Unlock()
		taskDir, setPriority, AfterViewer = oldDir, oldSet, oldAfter
	})
	return rec
}

func TestRaiseAndReleaseEveryThread(t *testing.T) {
	rec := setup(t, "100", "101", "not-a-thread")
	Raise()
	for _, tid := range []int{100, 101} {
		if v, _ := rec.get(tid); v != Boost {
			t.Fatalf("thread %d at %d after Raise", tid, v)
		}
	}
	Release()
	for _, tid := range []int{100, 101} {
		if v, _ := rec.get(tid); v != Normal {
			t.Fatalf("thread %d at %d after Release", tid, v)
		}
	}
	// A second release, or a viewer after it, changes nothing.
	rec.value[100] = 7
	Release()
	ViewerConnected()
	if v, _ := rec.get(100); v != 7 {
		t.Fatal("a later release touched the threads again")
	}
}

func TestViewerShortensTheBoost(t *testing.T) {
	setup(t, "100")
	Raise()
	ViewerConnected()
	mu.Lock()
	remaining := timer != nil
	mu.Unlock()
	if !remaining {
		t.Fatal("no release scheduled after the first viewer")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := released
		mu.Unlock()
		if done {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the boost did not end after the first viewer")
}

func TestWithoutPermissionTheServerStaysNormal(t *testing.T) {
	rec := setup(t, "100")
	rec.fail = unix.EPERM
	Raise()
	mu.Lock()
	r := raised
	mu.Unlock()
	if r {
		t.Fatal("Raise reported success without permission")
	}
	ViewerConnected()
	Release()
	if _, set := rec.get(100); set {
		t.Fatal("a failed raise still changed a thread later")
	}
}

func TestExitedThreadIsNotAnError(t *testing.T) {
	setup(t, "100")
	setPriority = func(int, int) error { return unix.ESRCH }
	if err := setAll(Boost); err != nil {
		t.Fatalf("ESRCH reported: %v", err)
	}
	setPriority = func(int, int) error { return errors.New("boom") }
	if err := setAll(Boost); err == nil {
		t.Fatal("a real error was hidden")
	}
}
