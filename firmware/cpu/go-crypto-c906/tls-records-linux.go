//go:build ignore

package main

import "syscall"

func cpuTime() (uint64, uint64) {
	var r syscall.Rusage
	must(syscall.Getrusage(syscall.RUSAGE_SELF, &r))
	return uint64(r.Utime.Nano()), uint64(r.Stime.Nano())
}
