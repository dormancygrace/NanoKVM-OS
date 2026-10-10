//go:build ignore

// Smoke test for NANOKVM_SYSMON_MIN_DELAY_US. It runs a 60 fps busy/idle
// loop shaped like the capture loop: wait for a frame in the netpoller (a
// pipe fed by a child process), then some work with a few blocking syscalls
// (default 4 ms; the first argument overrides it in microseconds). It counts
// voluntary context switches of the sysmon thread (each usleep or futex sleep
// is one) with the policy unset and at 1000/2000 us. The 1000 us child starts
// with GOMAXPROCS=2 (policy inactive) and switches to 1 at run time. Rounds
// are interleaved and compared by median, because other processes share the
// CPU. Timer slack is inherited from the caller's environment.
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
	envName  = "NANOKVM_SYSMON_MIN_DELAY_US"
	invalid  = "runtime: ignored invalid NanoKVM sysmon minimum delay"
	interval = time.Second / 60
	settle   = 1 * time.Second
	window   = 4 * time.Second
	rounds   = 3
)

func main() {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "ticker":
		ticker()
	case len(os.Args) > 3 && os.Args[1] == "measure":
		us, _ := strconv.Atoi(os.Args[3])
		measure(os.Args[2], time.Duration(us)*time.Microsecond)
	default:
		us := 4000
		if len(os.Args) > 1 {
			us, _ = strconv.Atoi(os.Args[1])
		}
		drive(strconv.Itoa(us))
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

func switches(tid string) (vol, invol int) {
	data, err := os.ReadFile("/proc/self/task/" + tid + "/status")
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "voluntary_ctxt_switches:" {
			vol, _ = strconv.Atoi(f[1])
		} else if len(f) == 2 && f[0] == "nonvoluntary_ctxt_switches:" {
			invol, _ = strconv.Atoi(f[1])
		}
	}
	return
}

func measure(phases string, work time.Duration) {
	cmd := exec.Command(os.Args[0], "ticker")
	frames, err := cmd.StdoutPipe() // pollable pipe: reads wait in the netpoller
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
	tid := sysmonTID()
	slack, _ := os.ReadFile("/proc/" + tid + "/timerslack_ns")
	buf := make([]byte, 64)
	sink := []byte{1}
	frame := func() {
		if _, err := frames.Read(buf); err != nil {
			panic(err)
		}
		start := time.Now()
		for i := 0; time.Since(start) < work; i++ {
			if i%512 == 0 {
				syscall.Write(null, sink) // blocking syscall: entersyscall/exitsyscall
			}
		}
	}
	for _, p := range strings.Split(phases, ",") {
		procs, _ := strconv.Atoi(p)
		runtime.GOMAXPROCS(procs)
		for end := time.Now().Add(settle); time.Now().Before(end); {
			frame()
		}
		v0, i0 := switches(tid)
		n, start := 0, time.Now()
		for time.Since(start) < window {
			frame()
			n++
		}
		v1, i1 := switches(tid)
		secs := time.Since(start).Seconds()
		fmt.Printf("gomaxprocs=%d fps=%.1f sysmon_vol/s=%.0f per_frame=%.2f sysmon_invol/s=%.0f slack_ns=%s\n",
			procs, float64(n)/secs, float64(v1-v0)/secs, float64(v1-v0)/float64(n),
			float64(i1-i0)/secs, strings.TrimSpace(string(slack)))
	}
}

// run starts one measuring child and returns its sysmon_vol/s per phase and
// its stderr.
func run(value *string, phases, work string) ([]float64, string) {
	cmd := exec.Command(os.Args[0], "measure", phases, work)
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
	var rates []float64
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fmt.Printf("%s=%-7s %s\n", envName, label, sc.Text())
		for _, field := range strings.Fields(sc.Text()) {
			if k, v, _ := strings.Cut(field, "="); k == "sysmon_vol/s" {
				rate, _ := strconv.ParseFloat(v, 64)
				rates = append(rates, rate)
			}
		}
	}
	return rates, stderr.String()
}

func ptr(s string) *string { return &s }

func median(v []float64) float64 {
	v = slices.Clone(v)
	slices.Sort(v)
	return v[len(v)/2]
}

func drive(work string) {
	fail := false
	check := func(ok bool, format string, args ...any) {
		if !ok {
			fail = true
			fmt.Printf("FAIL "+format+"\n", args...)
		}
	}
	fmt.Printf("work_us=%s rounds=%d\n", work, rounds)
	var base, empty, gmp2, floor1000, floor2000 []float64
	for i := 0; i < rounds; i++ {
		r, errs := run(nil, "1", work)
		check(errs == "", "unset: unexpected stderr %q", errs)
		base = append(base, r[0])
		r, errs = run(ptr(""), "1", work)
		check(errs == "", "empty: unexpected stderr %q", errs)
		empty = append(empty, r[0])
		r, errs = run(ptr("1000"), "2,1", work)
		check(errs == "", "1000: unexpected stderr %q", errs)
		gmp2 = append(gmp2, r[0])
		floor1000 = append(floor1000, r[1])
		r, errs = run(ptr("2000"), "1", work)
		check(errs == "", "2000: unexpected stderr %q", errs)
		floor2000 = append(floor2000, r[0])
	}
	_, errs := run(ptr("1500"), "1", work)
	check(strings.Contains(errs, invalid), "1500 was not rejected by the runtime hook: %q", errs)

	b, e, g, f1, f2 := median(base), median(empty), median(gmp2), median(floor1000), median(floor2000)
	fmt.Printf("median sysmon_vol/s: unset=%.0f empty=%.0f 1000@gomaxprocs2=%.0f 1000=%.0f 2000=%.0f\n", b, e, g, f1, f2)
	// The floor must reduce wakeups with one P, be inactive with two Ps and
	// follow the run-time GOMAXPROCS switch. Thresholds are loose because
	// the device scheduler already stretches 20 us sleeps under load.
	check(f1 < 0.85*b, "1000 us floor did not reduce wakeups: %.0f vs %.0f", f1, b)
	check(f2 < 1.1*f1, "2000 us floor woke more than 1000 us: %.0f vs %.0f", f2, f1)
	check(g > 1.15*f1, "floor active with GOMAXPROCS=2 or not applied after the switch: %.0f vs %.0f", g, f1)
	check(e > 0.7*b && e < 1.4*b, "empty value differs from unset: %.0f vs %.0f", e, b)
	if fail {
		os.Exit(1)
	}
	fmt.Println("PASS")
}
