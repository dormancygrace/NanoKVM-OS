package vm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

func hasZstdRecompression(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#1:") && strings.Contains(line, "[zstd]") {
			return true
		}
	}
	return false
}

func parseRecompressionSetting(text string) bool {
	enabled := false
	for _, line := range strings.Split(text, "\n") {
		switch line {
		case "ZRAM_RECOMPRESS=1":
			enabled = true
		case "ZRAM_RECOMPRESS=0":
			enabled = false
		}
	}
	return enabled
}

// RunMemoryMaintenance creates no second daemon. The sleeping server goroutine
// starts a low-priority helper only when the optional setting is enabled.
func RunMemoryMaintenance(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	runMemoryMaintenance(ctx, ticker.C, func(ctx context.Context) error {
		data, err := os.ReadFile("/etc/kvm/memory.conf")
		if err != nil || !parseRecompressionSetting(string(data)) {
			return nil
		}
		// S38memory takes the same flock as swap reconfiguration; contention is
		// a skipped cycle. Commands never overlap and stop with the server.
		swaps, err := os.ReadFile("/proc/swaps")
		if err != nil || !parseActiveSwaps(string(swaps))["/dev/zram0"].Enabled {
			return nil
		}
		output, err := exec.CommandContext(ctx, "nice", "-n", "19", "sh", memoryService, "recompress").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %.512s", err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}

func runMemoryMaintenance(ctx context.Context, ticks <-chan time.Time, run func(context.Context) error) {
	lastError := ""
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok || ctx.Err() != nil {
				return
			}
			err := run(ctx)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				lastError = ""
				continue
			}
			if err.Error() != lastError {
				log.Warnf("ZRAM recompression skipped/failed: %v", err)
				lastError = err.Error()
			}
		}
	}
}
