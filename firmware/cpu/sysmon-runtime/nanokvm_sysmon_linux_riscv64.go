// Copyright 2026 NanoKVM Enhanced contributors.
// Use of this source code is governed by a BSD-style license matching Go.

package runtime

import "internal/runtime/atomic"

var nanokvmSysmonSlack atomic.Uintptr
var nanokvmInheritedSlack atomic.Uintptr

// Minimum sysmon sleep in microseconds while GOMAXPROCS is 1; 0 disables it.
// Only the sysmon thread writes (once, at startup) and reads this word.
var nanokvmSysmonMinDelay uint32

// These startup hooks run without a P. They must not allocate or block.
func nanokvmSysmonInit() {
	nanokvmSysmonSlackInit()
	nanokvmSysmonMinDelayInit()
}

func nanokvmSysmonSlackInit() {
	var requested uintptr
	switch gogetenv("NANOKVM_SYSMON_TIMER_SLACK_NS") {
	case "", "0":
		return
	case "50000":
		requested = 50000
	case "250000":
		requested = 250000
	default:
		print("runtime: ignored invalid NanoKVM sysmon timer slack\n")
		return
	}
	inherited := nanokvmPrctl(30, 0) // PR_GET_TIMERSLACK
	if inherited <= 0 || nanokvmPrctl(29, requested) != 0 {
		print("runtime: could not set NanoKVM sysmon timer slack\n")
		return
	}
	// Publish the original value first. No child of sysmon can be created
	// before this startup hook returns.
	nanokvmInheritedSlack.Store(uintptr(inherited))
	nanokvmSysmonSlack.Store(requested)
}

func nanokvmSysmonMinDelayInit() {
	switch gogetenv("NANOKVM_SYSMON_MIN_DELAY_US") {
	case "", "0":
	case "500":
		nanokvmSysmonMinDelay = 500
	case "1000":
		nanokvmSysmonMinDelay = 1000
	case "2000":
		nanokvmSysmonMinDelay = 2000
	default:
		print("runtime: ignored invalid NanoKVM sysmon minimum delay\n")
	}
}

// nanokvmSysmonDelay returns the sleep for one sysmon iteration. sysmon's own
// delay/idle state is not modified, so its backoff is unchanged above the
// floor. gomaxprocs is read on every iteration (racily, like sysmon itself),
// so a runtime GOMAXPROCS change applies from the next iteration. Runs on
// sysmon without a P: no allocation, no write barriers.
//
//go:nosplit
func nanokvmSysmonDelay(delay uint32) uint32 {
	if floor := nanokvmSysmonMinDelay; floor > delay && gomaxprocs == 1 {
		return floor
	}
	return delay
}

func nanokvmThreadInit() {
	requested := nanokvmSysmonSlack.Load()
	inherited := nanokvmInheritedSlack.Load()
	if requested == 0 || requested == inherited {
		return
	}
	// Linux clone inherits timer slack. Do not propagate sysmon's tuning
	// into Go workers or the template thread used to create more workers.
	// Other inherited values retain their existing policy.
	if nanokvmPrctl(30, 0) == int64(requested) && nanokvmPrctl(29, inherited) != 0 {
		print("runtime: could not restore inherited NanoKVM timer slack\n")
	}
}

//go:noescape
func nanokvmPrctl(option, value uintptr) int64
