//go:build ignore

package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	runtime.GOMAXPROCS(2)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ready := make(chan struct{}, 12)
	release := make(chan struct{})
	for i := 0; i < 12; i++ {
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			ready <- struct{}{}
			<-release
		}()
	}
	for i := 0; i < 12; i++ {
		<-ready
	}
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		panic(err)
	}
	counts := map[string]int{}
	for _, entry := range entries {
		data, err := os.ReadFile("/proc/" + entry.Name() + "/timerslack_ns")
		if err != nil {
			panic(err)
		}
		counts[strings.TrimSpace(string(data))]++
	}
	expected := 0
	if os.Getenv("NANOKVM_SYSMON_TIMER_SLACK_NS") == "250000" {
		expected = 1
	}
	if counts["250000"] != expected || counts["50000"] < 13 || len(counts) > 1+expected {
		panic(fmt.Sprintf("unexpected thread timer slack: %v", counts))
	}
	value, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 30, 0, 0, 0, 0, 0)
	if errno != 0 || value != 50000 {
		panic("main thread timer slack changed")
	}
	close(release)
	fmt.Println("PASS threads="+strconv.Itoa(len(entries)), "slack_ns_counts=", counts)
}
