// Package startpriority runs the server ahead of the other services while it
// starts and the first viewer connects, then returns it to the normal
// priority.
//
// The boot is CPU-bound on the single C906 core: from the first service to the
// first frame the CPU is busy all the time, and the services that start after
// the application (chronyd, syslog, crond, sshd, the watchdog) compete with
// the server for it. At nice -10 the server reached its first frame about
// 2.3 s sooner on the stand (17.5 s after kernel start, from 19.6-20.2 s).
// Kept for good, the boost would also take CPU from SSH sessions and
// kvm_system while video streams, so it ends a few seconds after the first
// viewer, and a minute after the start at the latest.
//
// Linux sets the nice value per thread. Threads inherit it from the thread
// that creates them, so raising every thread at the start covers the Go
// runtime threads and the native video threads created later; releasing
// visits every thread again.
package startpriority

import (
	"os"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	// Boost is the nice value while the server starts.
	Boost = -10
	// Normal is the default nice value.
	Normal = 0
)

var (
	// AfterViewer is how long the boost lasts once a viewer connected: the
	// first frame follows within a second or two.
	AfterViewer = 5 * time.Second
	// Limit ends the boost when no viewer connects.
	Limit = time.Minute

	taskDir     = "/proc/self/task"
	setPriority = func(tid, value int) error { return unix.Setpriority(unix.PRIO_PROCESS, tid, value) }

	mu       sync.Mutex
	raised   bool
	released bool
	timer    *time.Timer
)

// Raise gives every thread of the process the boost and schedules its end
// after Limit. Raising requires CAP_SYS_NICE; without it the server runs at
// the normal priority.
func Raise() {
	mu.Lock()
	defer mu.Unlock()
	if raised {
		return
	}
	if err := setAll(Boost); err != nil {
		log.Warnf("start priority: %v", err)
		return
	}
	raised = true
	timer = time.AfterFunc(Limit, Release)
}

// ViewerConnected ends the boost AfterViewer from now, unless it ends sooner.
func ViewerConnected() {
	mu.Lock()
	defer mu.Unlock()
	if !raised || released || timer == nil {
		return
	}
	if timer.Stop() {
		timer = time.AfterFunc(AfterViewer, Release)
	}
}

// Release returns every thread to the normal priority. Later calls do nothing.
func Release() {
	mu.Lock()
	defer mu.Unlock()
	if !raised || released {
		return
	}
	released = true
	if timer != nil {
		timer.Stop()
	}
	if err := setAll(Normal); err != nil {
		log.Warnf("start priority: %v", err)
		return
	}
	log.Info("start priority released")
}

// setAll sets value on every thread. A thread created during the pass
// inherits the old value from its creator, so a second pass catches it.
func setAll(value int) error {
	var last error
	for pass := 0; pass < 2; pass++ {
		entries, err := os.ReadDir(taskDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			tid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			// A thread may exit between the listing and the call.
			if err := setPriority(tid, value); err != nil && err != unix.ESRCH {
				last = err
			}
		}
	}
	return last
}
