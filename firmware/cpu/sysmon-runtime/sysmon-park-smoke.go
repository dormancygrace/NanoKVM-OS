//go:build ignore

// Smoke test for NANOKVM_SYSMON_PARK_GRACE_MS. Each child first runs a
// 75 fps loop shaped like the video path (wait for a frame in the netpoller,
// then about 1.5 ms of work with a few blocking syscalls) and counts the
// sysmon thread's voluntary context switches. It then measures how late a
// 5 ms ticker runs while another goroutine blocks the only P in a 30 ms
// syscall every 97 ms: that lateness is bounded by how soon sysmon retakes
// the P. Rounds with the grace unset and at 100 ms are interleaved and
// compared by median; an invalid value must be rejected.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	envName  = "NANOKVM_SYSMON_PARK_GRACE_MS"
	invalid  = "runtime: ignored invalid NanoKVM sysmon park grace"
	interval = time.Second / 75
	work     = 1500 * time.Microsecond
	window   = 4 * time.Second
	rounds   = 3
)

func main() {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "ticker":
		ticker()
	case len(os.Args) > 1 && os.Args[1] == "measure":
		runtime.GOMAXPROCS(1)
		frames()
		lateness()
	default:
		drive()
	}
}

func ticker() {
	t := time.NewTicker(interval)
	for range t.C {
		if _, err := os.Stdout.Write([]byte{0}); err != nil {
			os.Exit(0)
		}
	}
}

// sysmonTID returns the first thread created after the main thread. The
// runtime starts sysmon before anything else on Linux.
func sysmonTID() string {
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		panic(err)
	}
	pid, best := os.Getpid(), 0
	for _, e := range entries {
		tid, _ := strconv.Atoi(e.Name())
		if tid != pid && (best == 0 || tid < best) {
			best = tid
		}
	}
	return strconv.Itoa(best)
}

func voluntary(tid string) int {
	data, err := os.ReadFile("/proc/self/task/" + tid + "/status")
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "voluntary_ctxt_switches:" {
			n, _ := strconv.Atoi(f[1])
			return n
		}
	}
	return 0
}

func frames() {
	cmd := exec.Command(os.Args[0], "ticker")
	pipe, err := cmd.StdoutPipe() // pollable pipe: reads wait in the netpoller
	if err != nil {
		panic(err)
	}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	defer cmd.Process.Kill()
	null, err := syscall.Open("/dev/null", syscall.O_WRONLY, 0)
	if err != nil {
		panic(err)
	}
	tid, buf, sink := sysmonTID(), make([]byte, 64), []byte{1}
	frame := func() {
		if _, err := pipe.Read(buf); err != nil {
			panic(err)
		}
		start := time.Now()
		for i := 0; time.Since(start) < work; i++ {
			if i%512 == 0 {
				syscall.Write(null, sink) // blocking syscall: entersyscall/exitsyscall
			}
		}
	}
	for end := time.Now().Add(time.Second); time.Now().Before(end); {
		frame()
	}
	v0, n, start := voluntary(tid), 0, time.Now()
	for time.Since(start) < window {
		frame()
		n++
	}
	secs := time.Since(start).Seconds()
	fmt.Printf("fps=%.1f sysmon_vol/s=%.0f\n", float64(n)/secs, float64(voluntary(tid)-v0)/secs)
}

func lateness() {
	go func() {
		for {
			time.Sleep(97 * time.Millisecond)
			ts := syscall.Timespec{Nsec: 30e6}
			syscall.Nanosleep(&ts, nil) // keeps the only P until sysmon retakes it
		}
	}()
	const period = 5 * time.Millisecond
	var late []time.Duration
	end, next := time.Now().Add(10*time.Second), time.Now().Add(period)
	for time.Now().Before(end) {
		time.Sleep(time.Until(next))
		late = append(late, time.Since(next))
		if next = next.Add(period); next.Before(time.Now()) {
			next = time.Now().Add(period)
		}
	}
	slices.Sort(late)
	fmt.Printf("late_p99_ms=%.2f late_max_ms=%.2f\n",
		late[len(late)*99/100].Seconds()*1e3, late[len(late)-1].Seconds()*1e3)
}

// run starts one measuring child and returns its numeric fields and stderr.
func run(value *string) (map[string]float64, string) {
	cmd := exec.Command(os.Args[0], "measure")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, envName+"=") && !strings.HasPrefix(kv, "GOMAXPROCS=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	label := "unset"
	if value != nil {
		cmd.Env = append(cmd.Env, envName+"="+*value)
		label = strconv.Quote(*value)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		panic(fmt.Sprintf("%s: %v\n%s", label, err, stderr.String()))
	}
	fields := map[string]float64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fmt.Printf("%s=%-7s %s\n", envName, label, sc.Text())
		for _, field := range strings.Fields(sc.Text()) {
			if k, v, ok := strings.Cut(field, "="); ok {
				fields[k], _ = strconv.ParseFloat(v, 64)
			}
		}
	}
	return fields, stderr.String()
}

func ptr(s string) *string { return &s }

func median(v []float64) float64 {
	v = slices.Clone(v)
	slices.Sort(v)
	return v[len(v)/2]
}

func drive() {
	fail := false
	check := func(ok bool, format string, args ...any) {
		if !ok {
			fail = true
			fmt.Printf("FAIL "+format+"\n", args...)
		}
	}
	var baseRate, graceRate, baseLate, graceLate []float64
	for i := 0; i < rounds; i++ {
		r, errs := run(nil)
		check(errs == "", "unset: unexpected stderr %q", errs)
		baseRate, baseLate = append(baseRate, r["sysmon_vol/s"]), append(baseLate, r["late_max_ms"])
		r, errs = run(ptr("100"))
		check(errs == "", "100: unexpected stderr %q", errs)
		graceRate, graceLate = append(graceRate, r["sysmon_vol/s"]), append(graceLate, r["late_max_ms"])
	}
	_, errs := run(ptr("75"))
	check(strings.Contains(errs, invalid), "75 was not rejected by the runtime hook: %q", errs)

	br, gr, bl, gl := median(baseRate), median(graceRate), median(baseLate), median(graceLate)
	fmt.Printf("median sysmon_vol/s: unset=%.0f 100=%.0f; median late_max_ms: unset=%.1f 100=%.1f\n", br, gr, bl, gl)
	// The grace must cut the per-frame park/wake cycle. While quiet, sysmon
	// sleeps 10 ms, so a P blocked in a syscall is retaken within about two
	// such sleeps plus timer slack.
	check(gr < 0.8*br, "100 ms grace did not reduce sysmon wakeups: %.0f vs %.0f", gr, br)
	check(gl < 25, "retake latency with the grace exceeds 25 ms: %.1f", gl)
	if fail {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
