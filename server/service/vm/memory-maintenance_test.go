package vm

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRecompressionSettingAndRequest(t *testing.T) {
	if !hasZstdRecompression("#1: lz4 [zstd]\n") || hasZstdRecompression("#2: lz4 [zstd]\n") {
		t.Fatal("readiness must identify priority 1, which the worker uses")
	}
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"", false}, {"ZRAM_RECOMPRESS=1\n", true},
		{"ZRAM_RECOMPRESS=1\nZRAM_RECOMPRESS=0\n", false},
		{"ZRAM_RECOMPRESS=1\nZRAM_RECOMPRESS=invalid\n", true},
		{"ZRAM_RECOMPRESS=$(touch /tmp/unwanted)", false},
		{"ZRAM_RECOMPRESS=10", false}, {"SD_ENABLED=1", false},
	} {
		if got := parseRecompressionSetting(tc.text); got != tc.want {
			t.Fatalf("%q: %t", tc.text, got)
		}
	}
	enabled := true
	if validSwapRequest(memorySwapRequest{Kind: "sd", SizeMiB: 256, Recompress: &enabled}) {
		t.Fatal("SD accepted a ZRAM-only setting")
	}
	if !validSwapRequest(memorySwapRequest{Kind: "zram", SizeMiB: 64, Recompress: &enabled}) {
		t.Fatal("ZRAM setting rejected")
	}
}

func TestZRAMRecompressionChecksAndLock(t *testing.T) {
	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }
	z := zramRecompression{config: path("memory.conf"), swaps: path("swaps"), meminfo: path("meminfo"),
		uptime: path("uptime"), mmStat: path("mm_stat"), idle: path("idle"),
		recompress: path("recompress"), lock: path("lock")}
	write := func(name, text string) {
		if err := os.WriteFile(path(name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	written := func() string {
		idle, _ := os.ReadFile(z.idle)
		recompress, _ := os.ReadFile(z.recompress)
		write("idle", "")
		write("recompress", "")
		return string(idle) + "|" + string(recompress)
	}
	write("memory.conf", "ZRAM_ENABLED=1\nZRAM_RECOMPRESS=1\n")
	write("swaps", "Filename Type Size Used Priority\n/dev/zram0 partition 53244 6000 100\n")
	write("meminfo", "MemTotal: 106496 kB\nMemAvailable: 46000 kB\n")
	write("uptime", "2400.50 2000.00\n")
	write("idle", "")
	write("recompress", "")
	for _, tc := range []struct {
		name, mmStat, want string
	}{
		{"small swap", "5550080 1121896 2891776 0 7659520 0 1292 66 3252\n", "|"},
		{"enough to recompress", "9550080 1121896 2891776 0 7659520 0 1292 66 3252\n",
			"60|type=idle priority=1 max_pages=256"},
	} {
		write("mm_stat", tc.mmStat)
		if err := z.run(context.Background()); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := written(); got != tc.want {
			t.Fatalf("%s: wrote %q", tc.name, got)
		}
	}
	held, err := os.OpenFile(z.lock, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := z.run(context.Background()); err != nil || written() != "|" {
		t.Fatal("a held memory lock must skip the cycle")
	}
	syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
	write("memory.conf", "ZRAM_RECOMPRESS=0\n")
	if err := z.run(context.Background()); err != nil || written() != "|" {
		t.Fatal("recompression ran while disabled")
	}
}

func TestMemoryMaintenanceCancellationReachesActiveCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	started, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		runMemoryMaintenance(ctx, ticks, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	ticks <- time.Now()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("command did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker ignored cancellation")
	}
}

func TestMemoryMaintenanceDoesNotRunAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	runMemoryMaintenance(ctx, ticks, func(context.Context) error { t.Error("started after shutdown"); return nil })
}
