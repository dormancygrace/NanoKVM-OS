package apkrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

// A run that loads one small index states its smaller need.
func TestMemoryOverride(t *testing.T) {
	f := newFixture(t, "")
	writeMemInfo(t, 20*1024, 40*1024, 14*1024) // 27 MiB budget
	cmd := Command(context.Background(), f.apk, "--repositories-file=/dev/null", "version")
	cmd.MemoryKiB = 12 * 1024
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("small run refused: %q %v", output, err)
	}
	cmd = Command(context.Background(), f.apk, "version")
	cmd.MemoryKiB = 40 * 1024
	var low *LowMemoryError
	if _, err := cmd.CombinedOutput(); !errors.As(err, &low) || low.RequiredKiB != 40*1024 {
		t.Fatalf("err %v", err)
	}
}

func TestMeasuredDeviceMemoryAllowsOneRun(t *testing.T) {
	newFixture(t, "")
	// /proc/meminfo on the device with the server idle (2026-10-07).
	writeMemInfo(t, 50452, 47048, 13712)
	if err := checkMemory(requiredKiB); err != nil {
		t.Fatal(err)
	}
	// The same device while another index load was at its peak.
	writeMemInfo(t, 9204, 44332, 13712+34164)
	if err := checkMemory(requiredKiB); err == nil {
		t.Fatal("a second index load was allowed during the first one's peak")
	}
}
