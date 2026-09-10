// Copyright 2026 NanoKVM Enhanced contributors.
// Use of this source code is governed by a BSD-style license matching Go.

package runtime

import "internal/runtime/atomic"

var nanokvmSysmonSlack atomic.Uintptr
var nanokvmInheritedSlack atomic.Uintptr

// These startup hooks run without a P. They must not allocate or block.
func nanokvmSysmonInit() {
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
