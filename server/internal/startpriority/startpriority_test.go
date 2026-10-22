package startpriority

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// recorder stands in for setpriority and keeps each thread's stat line, whose
// nice field Release reads.
type recorder struct {
	mu    sync.Mutex
	dir   string
	calls int
	fail  error
}

func (r *recorder) write(tid, value int) {
	line := fmt.Sprintf("%d (thread name) S%s %d 1 0\n", tid, strings.Repeat(" 0", 15), value)
	if err := os.WriteFile(filepath.Join(r.dir, strconv.Itoa(tid), "stat"), []byte(line), 0o644); err != nil {
		panic(err)
	}
}

func (r *recorder) set(tid, value int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.fail != nil {
		return r.fail
	}
	r.write(tid, value)
	return nil
}

func setup(t *testing.T, tids ...int) *recorder {
	t.Helper()
	rec := &recorder{dir: t.TempDir()}
	for _, tid := range tids {
		if err := os.Mkdir(filepath.Join(rec.dir, strconv.Itoa(tid)), 0o755); err != nil {
			t.Fatal(err)
		}
		rec.write(tid, Normal)
	}
	if err := os.Mkdir(filepath.Join(rec.dir, "not-a-thread"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldDir, oldSet, oldAfter := taskDir, setPriority, AfterViewer
	taskDir, setPriority, AfterViewer = rec.dir, rec.set, 100*time.Millisecond
	mu.Lock()
	raised, shortened, released, timer = false, false, false, nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		raised, shortened, released, timer = false, false, false, nil
		mu.Unlock()
		taskDir, setPriority, AfterViewer = oldDir, oldSet, oldAfter
	})
	return rec
}

func TestRaiseAndReleaseEveryBoostedThread(t *testing.T) {
	rec := setup(t, 100, 101, 102)
	Raise()
	for _, tid := range []int{100, 101, 102} {
		if v := niceOf(tid); v != Boost {
			t.Fatalf("thread %d at %d after Raise", tid, v)
		}
	}
	// Something else changed one thread's priority meanwhile: keep it.
	rec.write(102, 7)
	Release()
	for tid, want := range map[int]int{100: Normal, 101: Normal, 102: 7} {
		if v := niceOf(tid); v != want {
			t.Fatalf("thread %d at %d after Release, want %d", tid, v, want)
		}
	}
	// A second release, or a viewer after it, changes nothing.
	rec.write(100, Boost)
	Release()
	ViewerConnected()
	if niceOf(100) != Boost {
		t.Fatal("a later release touched the threads again")
	}
}

func TestOnlyTheFirstViewerShortensTheBoost(t *testing.T) {
	setup(t, 100)
	Raise()
	ViewerConnected()
	mu.Lock()
	first := timer
	mu.Unlock()
	// A client that reconnects must not move the end again.
	ViewerConnected()
	mu.Lock()
	same := timer == first
	mu.Unlock()
	if !same {
		t.Fatal("a later viewer rescheduled the end of the boost")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := released
		mu.Unlock()
		if done {
			if niceOf(100) != Normal {
				t.Fatal("thread still boosted")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the boost did not end after the first viewer")
}

func TestWithoutPermissionNothingChanges(t *testing.T) {
	rec := setup(t, 100)
	rec.fail = unix.EPERM
	Raise()
	rec.fail = nil
	calls := rec.calls
	Release()
	if rec.calls != calls || niceOf(100) != Normal {
		t.Fatal("Release changed a thread that was never boosted")
	}
}

func TestNiceOfReadsTheStatLine(t *testing.T) {
	rec := setup(t, 100)
	rec.write(100, -10)
	if v := niceOf(100); v != -10 {
		t.Fatalf("nice %d", v)
	}
	if v := niceOf(999); v != Normal {
		t.Fatalf("a missing thread reads as %d", v)
	}
}

func TestExitedThreadIsNotAnError(t *testing.T) {
	setup(t, 100)
	setPriority = func(int, int) error { return unix.ESRCH }
	if err := setAll(Boost, nil); err != nil {
		t.Fatalf("ESRCH reported: %v", err)
	}
	setPriority = func(int, int) error { return errors.New("boom") }
	if err := setAll(Boost, nil); err == nil {
		t.Fatal("a real error was hidden")
	}
}
