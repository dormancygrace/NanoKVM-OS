package apkrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The helper process runs one apk query through the runner, so the lock is
// exercised between processes as well as between goroutines.
func TestMain(m *testing.M) {
	if os.Getenv("APKRUN_HELPER") == "1" {
		LockPath = os.Getenv("APKRUN_LOCK")
		output, err := Command(context.Background(), os.Getenv("APKRUN_APK"), "search", "helper").CombinedOutput()
		if err != nil {
			fmt.Fprintln(os.Stderr, string(output), err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fixture struct {
	dir, apk string
}

// newFixture installs a fake apk that fails if another one is running and
// records each run, and points the runner at a private lock and meminfo.
func newFixture(t *testing.T, script string) fixture {
	t.Helper()
	dir := t.TempDir()
	oldLock, oldMem, oldPoll := LockPath, memInfoPath, lockPoll
	t.Cleanup(func() { LockPath, memInfoPath, lockPoll = oldLock, oldMem, oldPoll })
	LockPath = filepath.Join(dir, "apk.lock")
	memInfoPath = filepath.Join(dir, "meminfo")
	lockPoll = 10 * time.Millisecond
	writeMemInfo(t, 60000, 40000, 14000)
	apk := filepath.Join(dir, "apk")
	if script == "" {
		script = `if ! mkdir "$DIR/running" 2>/dev/null; then echo overlap >> "$DIR/overlaps"; fi
echo "$*" >> "$DIR/runs"
sleep 0.15
rmdir "$DIR/running" 2>/dev/null
echo "ran $*"`
	}
	content := "#!/bin/sh\nDIR=" + dir + "\n" + script + "\n"
	if err := os.WriteFile(apk, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	return fixture{dir: dir, apk: apk}
}

func writeMemInfo(t *testing.T, available, swapFree, anon int64) {
	t.Helper()
	text := fmt.Sprintf("MemTotal:  106924 kB\nMemAvailable: %d kB\nAnonPages: %d kB\nSwapFree: %d kB\n", available, anon, swapFree)
	if err := os.WriteFile(memInfoPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) lines(name string) []string {
	data, _ := os.ReadFile(filepath.Join(f.dir, name))
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want class
	}{
		{[]string{"info", "-e", "nanokvm-rustdesk"}, installedOnly},
		{[]string{"info", "-e", "-v", "nanokvm-rustdesk"}, installedOnly},
		{[]string{"info", "--verbose"}, indexQuery},
		{[]string{"query", "--no-network", "--from=repositories", "x"}, indexQuery},
		{[]string{"search", "--all", "*mc*"}, indexQuery},
		{[]string{"version", "-l", "<"}, indexQuery},
		{[]string{"update"}, indexQuery},
		{[]string{"del", "--simulate", "--", "mc"}, indexQuery},
		{[]string{"add", "--", "mc"}, transaction},
		{[]string{"del", "--", "mc"}, transaction},
		{[]string{"upgrade"}, transaction},
		{[]string{"fix"}, transaction},
		{[]string{"--no-network", "add", "x"}, transaction},
		{nil, indexQuery},
	} {
		if got := classify(tc.args); got != tc.want {
			t.Errorf("classify(%q) = %d, want %d", tc.args, got, tc.want)
		}
	}
}

func TestRunsDoNotOverlapAcrossGoroutines(t *testing.T) {
	f := newFixture(t, "")
	var wait sync.WaitGroup
	for i := 0; i < 5; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			args := []string{"search", fmt.Sprint(i)}
			if i%2 == 1 {
				args = []string{"add", "--", fmt.Sprint(i)}
			}
			output, err := Command(context.Background(), f.apk, args...).CombinedOutput()
			if err != nil || !strings.HasPrefix(string(output), "ran ") {
				t.Errorf("run %d: %q %v", i, output, err)
			}
		}(i)
	}
	wait.Wait()
	if overlaps := f.lines("overlaps"); len(overlaps) != 0 {
		t.Fatalf("%d apk runs overlapped", len(overlaps))
	}
	if runs := f.lines("runs"); len(runs) != 5 {
		t.Fatalf("runs: %q", runs)
	}
}

func TestRunsDoNotOverlapAcrossProcesses(t *testing.T) {
	f := newFixture(t, "")
	helpers := make([]*exec.Cmd, 3)
	for i := range helpers {
		helpers[i] = exec.Command(os.Args[0], "-test.run=^$")
		helpers[i].Env = append(os.Environ(), "APKRUN_HELPER=1", "APKRUN_LOCK="+LockPath, "APKRUN_APK="+f.apk)
		helpers[i].Stderr = os.Stderr
		if err := helpers[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := Command(context.Background(), f.apk, "update").CombinedOutput(); err != nil {
			t.Fatal(err)
		}
	}
	for _, helper := range helpers {
		if err := helper.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if overlaps := f.lines("overlaps"); len(overlaps) != 0 {
		t.Fatalf("%d apk runs overlapped", len(overlaps))
	}
	if runs := f.lines("runs"); len(runs) != 6 {
		t.Fatalf("runs: %q", runs)
	}
}

func TestLockWaitEndsWithBusy(t *testing.T) {
	f := newFixture(t, "")
	held, err := acquire(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	cmd := Command(context.Background(), f.apk, "search", "x")
	cmd.LockWait = 50 * time.Millisecond
	output, err := cmd.CombinedOutput()
	if !errors.Is(err, ErrBusy) || !NotStarted(err) || string(output) != ErrBusy.Error() {
		t.Fatalf("output %q err %v", output, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err = Command(ctx, f.apk, "add", "x").CombinedOutput(); !errors.Is(err, ErrBusy) {
		t.Fatalf("context deadline: %v", err)
	}
	// Installed-database queries do not wait for index loads or transactions.
	if output, err = Command(context.Background(), f.apk, "info", "-e", "x").CombinedOutput(); err != nil {
		t.Fatalf("installed query: %q %v", output, err)
	}
	if runs := f.lines("runs"); len(runs) != 1 || runs[0] != "info -e x" {
		t.Fatalf("runs: %q", runs)
	}
}

func TestQueuedRunGetsItsOwnTimeout(t *testing.T) {
	f := newFixture(t, "")
	held, err := acquire(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(300*time.Millisecond, func() { held.Close() })
	cmd := Command(context.Background(), f.apk, "search", "x")
	cmd.Timeout = 250 * time.Millisecond // shorter than the wait, longer than the run
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%q %v", output, err)
	}
}

func TestLowMemoryRefusesIndexLoads(t *testing.T) {
	f := newFixture(t, "")
	// 20 MiB available plus half of 14 MiB of swappable anonymous memory.
	writeMemInfo(t, 20*1024, 40*1024, 14*1024)
	var log strings.Builder
	cmd := Command(context.Background(), f.apk, "upgrade")
	cmd.Stdout, cmd.Stderr = &log, &log
	err := cmd.Run()
	var low *LowMemoryError
	if !errors.As(err, &low) || !NotStarted(err) || low.AvailableKiB != 27*1024 || low.RequiredKiB != requiredKiB {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(log.String(), "not enough free memory to run APK safely: about 27 MiB available, 32 MiB needed") {
		t.Fatalf("log %q", log.String())
	}
	if output, err := Command(context.Background(), f.apk, "info", "-e", "x").CombinedOutput(); err != nil {
		t.Fatalf("installed query refused: %q %v", output, err)
	}
	// Swap only helps as far as other processes have pages to swap out.
	writeMemInfo(t, 20*1024, 40*1024, 30*1024)
	if output, err := Command(context.Background(), f.apk, "update").CombinedOutput(); err != nil {
		t.Fatalf("enough memory refused: %q %v", output, err)
	}
	if err := os.Remove(memInfoPath); err != nil {
		t.Fatal(err)
	}
	if output, err := Command(context.Background(), f.apk, "update").CombinedOutput(); err != nil {
		t.Fatalf("unknown memory refused: %q %v", output, err)
	}
	if runs := f.lines("runs"); len(runs) != 3 {
		t.Fatalf("runs: %q", runs)
	}
}

func TestMeasuredDeviceMemoryAllowsOneRun(t *testing.T) {
	newFixture(t, "")
	// /proc/meminfo on the device with the server idle (2026-10-07).
	writeMemInfo(t, 50452, 47048, 13712)
	if err := checkMemory(); err != nil {
		t.Fatal(err)
	}
	// The same device while another index load was at its peak.
	writeMemInfo(t, 9204, 44332, 13712+34164)
	if err := checkMemory(); err == nil {
		t.Fatal("a second index load was allowed during the first one's peak")
	}
}

func TestChildPriorityAndOOMScore(t *testing.T) {
	f := newFixture(t, `sleep 0.2
echo "$(cut -d' ' -f19 /proc/$$/stat) $(cat /proc/$$/oom_score_adj)"`)
	own, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Skip(err)
	}
	fields := strings.Fields(string(own[strings.LastIndexByte(string(own), ')')+2:]))
	ownNice := fields[16]
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"query", "x"}, "19 1000"},
		{[]string{"add", "x"}, ownNice + " 1000"},
		{[]string{"info", "-e", "x"}, ownNice + " 1000"},
	} {
		output, err := Command(context.Background(), f.apk, tc.args...).CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != tc.want {
			t.Errorf("%q: %q %v, want %q", tc.args, output, err, tc.want)
		}
	}
}

func TestRunReportsApkFailure(t *testing.T) {
	f := newFixture(t, `echo "ERROR: unable to select packages" >&2; exit 2`)
	output, err := Command(context.Background(), f.apk, "add", "x").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || NotStarted(err) || !strings.Contains(string(output), "unable to select") {
		t.Fatalf("%q %v", output, err)
	}
}
