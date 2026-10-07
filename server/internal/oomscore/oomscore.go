// Package oomscore sets how strongly the kernel OOM killer prefers a process.
//
// With the 4K video memory mode Linux has about 104 MiB and a zram swap of half
// that. The OOM killer picks the process with the highest badness: its
// resident and swapped pages plus oom_score_adj thousandths of RAM+swap. An
// adjustment of -300 is worth about 47 MiB there: the web server, which also
// owns video capture and normally stays below 40 MiB, then outlives other
// processes unless it grows far beyond its normal footprint, when the kernel
// may still kill it (-1000 would forbid that and leave a runaway server
// holding the device). Helpers such as apk get 1000 and go first.
//
// The value is inherited by children, so a protected process resets it for
// children that run arbitrary or long-lived programs.
package oomscore

import (
	"os"
	"strconv"
	"strings"
)

const (
	// Critical is used for NanoKVM-Server and kvm_system.
	Critical = -300
	// Normal is the kernel default.
	Normal = 0
	// Expendable makes a helper the first OOM victim.
	Expendable = 1000
)

var procRoot = "/proc"

// ResetPrefix resets the adjustment of a sh -c script before it runs.
const ResetPrefix = "echo 0 >/proc/self/oom_score_adj; "

// Unprotected returns the command line that runs name with args at the
// normal adjustment. A protected process uses it for daemons it starts (for
// example through rc-service): the shell resets its own value and then
// becomes the program, so nothing the program starts inherits the protection.
func Unprotected(name string, args ...string) []string {
	return append([]string{"/bin/sh", "-c", ResetPrefix + `exec "$0" "$@"`, name}, args...)
}

// Set writes the adjustment for pid; pid 0 means the calling process. Raising
// it is always allowed; lowering it requires CAP_SYS_RESOURCE.
func Set(pid, value int) error {
	target := "self"
	if pid != 0 {
		target = strconv.Itoa(pid)
	}
	return os.WriteFile(procRoot+"/"+target+"/oom_score_adj", []byte(strconv.Itoa(value)), 0)
}

// Get reads the adjustment for pid; pid 0 means the calling process.
func Get(pid int) (int, error) {
	target := "self"
	if pid != 0 {
		target = strconv.Itoa(pid)
	}
	data, err := os.ReadFile(procRoot + "/" + target + "/oom_score_adj")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}
