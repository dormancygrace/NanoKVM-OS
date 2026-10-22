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
	"fmt"
	"os"
	"strconv"
	"strings"
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

	mu        sync.Mutex
	raised    bool
	shortened bool
	released  bool
	timer     *time.Timer
	deadline  time.Time
)

// Raise gives every thread of the process the boost and schedules its end
// after Limit. Raising requires CAP_SYS_NICE; without it the server runs at
// the normal priority. Every start of the server raises it, not only the
// boot.
func Raise() {
	mu.Lock()
	defer mu.Unlock()
	if raised {
		return
	}
	deadline = time.Now().Add(Limit)
	// Even after a partial failure: Release resets only boosted threads.
	if err := setAll(Boost, nil); err != nil {
		log.Warnf("start priority: %v", err)
	}
	raised = true
	timer = time.AfterFunc(time.Until(deadline), Release)
}

// ViewerConnected ends the boost AfterViewer after the first viewer. Later
// viewers change nothing, so a client that reconnects again and again cannot
// keep the boost beyond that.
func ViewerConnected() {
	mu.Lock()
	defer mu.Unlock()
	if !raised || released || shortened {
		return
	}
	shortened = true
	if time.Until(deadline) > AfterViewer && timer.Stop() {
		timer = time.AfterFunc(AfterViewer, Release)
	}
}

// Release returns the boosted threads to the normal priority, leaving any
// other value alone. Later calls do nothing.
func Release() {
	mu.Lock()
	defer mu.Unlock()
	if !raised || released {
		return
	}
	timer.Stop()
	if err := setAll(Normal, func(tid int) bool { return niceOf(tid) == Boost }); err != nil {
		log.Warnf("start priority release: %v; retrying", err)
		timer = time.AfterFunc(time.Second, Release)
		return
	}
	released = true
	log.Info("start priority released")
}

// niceOf reads a thread's nice value (field 19 of its stat line); it is
// Normal when the thread is gone or the line unreadable.
func niceOf(tid int) int {
	data, err := os.ReadFile(taskDir + "/" + strconv.Itoa(tid) + "/stat")
	if err != nil {
		return Normal
	}
	// The command name in parentheses may contain spaces.
	line := string(data)
	if i := strings.LastIndexByte(line, ')'); i >= 0 {
		line = line[i+1:]
	}
	fields := strings.Fields(line)
	// After the name: state(3) ... nice(19), the 17th field here.
	if len(fields) < 17 {
		return Normal
	}
	nice, err := strconv.Atoi(fields[16])
	if err != nil {
		return Normal
	}
	return nice
}

// setAll sets value on every thread that wants it (all when want is nil). A
// thread created during a pass can inherit the old value. Keep scanning until
// a pass changes nothing. Bound work under mu; Release retries if creation
// continues across all passes or a syscall fails.
func setAll(value int, want func(tid int) bool) error {
	var last error
	for pass := 0; pass < 8; pass++ {
		changed := false
		entries, err := os.ReadDir(taskDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			tid, err := strconv.Atoi(entry.Name())
			if err != nil || niceOf(tid) == value || want != nil && !want(tid) {
				continue
			}
			// A thread may exit between the listing and the call.
			if err := setPriority(tid, value); err == nil {
				changed = true
			} else if err != unix.ESRCH {
				last = err
			}
		}
		if last != nil || !changed {
			return last
		}
	}
	return fmt.Errorf("threads still changing priority after 8 passes")
}
