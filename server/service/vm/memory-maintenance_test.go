package vm

import (
	"context"
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
