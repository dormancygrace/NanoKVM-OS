//go:build nanokvm_profile

package main

import (
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"time"
)

// Diagnostic builds can capture one bounded CPU profile without an HTTP
// listener. The root-controlled path is opt-in; ordinary builds omit this code.
func startDiagnosticCPUProfile() {
	startDiagnosticTrace()
	path := os.Getenv("NANOKVM_CPU_PROFILE_PATH")
	if path == "" {
		return
	}
	duration := diagnosticSeconds("NANOKVM_CPU_PROFILE_SECONDS", 30, 120)
	delay := diagnosticSeconds("NANOKVM_CPU_PROFILE_DELAY_SECONDS", 15, 120)
	go func() {
		time.Sleep(delay)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			log.Printf("CPU profile: %v", err)
			return
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if err = pprof.StartCPUProfile(f); err != nil {
			f.Close()
			os.Remove(path)
			log.Printf("CPU profile: %v", err)
			return
		}
		time.Sleep(duration)
		pprof.StopCPUProfile()
		runtime.ReadMemStats(&after)
		if err = f.Close(); err != nil {
			log.Printf("CPU profile close: %v", err)
		}
		log.Printf("CPU profile complete: duration=%s allocated_bytes=%d allocations=%d gc_cycles=%d gc_pause_ns=%d",
			duration, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs,
			after.NumGC-before.NumGC, after.PauseTotalNs-before.PauseTotalNs)
	}()
}

// An execution trace shows every goroutine wakeup, syscall and preemption with
// its stack, which a CPU profile cannot attribute on riscv64 (no frame
// pointers for perf). Keep it short: traces grow quickly.
func startDiagnosticTrace() {
	path := os.Getenv("NANOKVM_TRACE_PATH")
	if path == "" {
		return
	}
	duration := diagnosticSeconds("NANOKVM_TRACE_SECONDS", 5, 30)
	delay := diagnosticSeconds("NANOKVM_TRACE_DELAY_SECONDS", 15, 120)
	go func() {
		time.Sleep(delay)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			log.Printf("trace: %v", err)
			return
		}
		if err = trace.Start(f); err != nil {
			f.Close()
			os.Remove(path)
			log.Printf("trace: %v", err)
			return
		}
		time.Sleep(duration)
		trace.Stop()
		if err = f.Close(); err != nil {
			log.Printf("trace close: %v", err)
		}
	}()
}

func diagnosticSeconds(name string, fallback, maximum int) time.Duration {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil || n < 0 || n > maximum {
		n = fallback
	}
	return time.Duration(n) * time.Second
}
