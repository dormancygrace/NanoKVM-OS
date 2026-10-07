package vm

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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

// zramRecompression names the files of S38memory recompress; tests point them
// into a temporary directory.
type zramRecompression struct {
	config, swaps, meminfo, uptime, mmStat, idle, recompress, lock string
}

var zramFiles = zramRecompression{
	config:     "/etc/kvm/memory.conf",
	swaps:      "/proc/swaps",
	meminfo:    "/proc/meminfo",
	uptime:     "/proc/uptime",
	mmStat:     "/sys/block/zram0/mm_stat",
	idle:       "/sys/block/zram0/idle",
	recompress: "/sys/block/zram0/recompress",
	lock:       "/run/nanokvm-memory.lock",
}

// RunMemoryMaintenance creates no second daemon. Each cycle used to start
// nice, sh and seven helpers only to find, almost always, nothing to
// recompress; the checks and sysfs writes of S38memory recompress now run in
// the server under the same lock.
func RunMemoryMaintenance(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	runMemoryMaintenance(ctx, ticker.C, zramFiles.run)
}

func (z zramRecompression) run(ctx context.Context) error {
	data, err := os.ReadFile(z.config)
	if err != nil || !parseRecompressionSetting(string(data)) {
		return nil
	}
	swaps, err := os.ReadFile(z.swaps)
	if err != nil || !parseActiveSwaps(string(swaps))["/dev/zram0"].Enabled {
		return nil
	}
	// S38memory takes the same flock as swap reconfiguration; contention is
	// a skipped cycle.
	lock, err := os.OpenFile(z.lock, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	// Avoid work on tiny swaps or when reclaim is already short of workspace.
	if !z.worthwhile() {
		return nil
	}
	idle, err := os.OpenFile(z.idle, os.O_WRONLY, 0)
	if err != nil {
		return errors.New("ZRAM recompression is unavailable")
	}
	defer idle.Close()
	recompress, err := os.OpenFile(z.recompress, os.O_WRONLY, 0)
	if err != nil {
		return errors.New("ZRAM recompression is unavailable")
	}
	defer recompress.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		// Nice is per thread on Linux. The goroutine exits locked, so the
		// runtime retires the thread instead of reusing it at nice 19.
		runtime.LockOSThread()
		if err := syscall.Setpriority(syscall.PRIO_PROCESS, syscall.Gettid(), 19); err != nil {
			done <- err
			return
		}
		if _, err := idle.WriteString("60"); err != nil {
			done <- err
			return
		}
		// max_pages bounds compression attempts, not the preceding scan.
		_, err := recompress.WriteString("type=idle priority=1 max_pages=256")
		done <- err
	}()
	return <-done
}

func (z zramRecompression) worthwhile() bool {
	meminfo, err1 := os.ReadFile(z.meminfo)
	mmStat, err2 := os.ReadFile(z.mmStat)
	uptime, err3 := os.ReadFile(z.uptime)
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	available := int64(-1)
	for _, line := range strings.Split(string(meminfo), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "MemAvailable:" {
			available, _ = strconv.ParseInt(fields[1], 10, 64)
		}
	}
	first := func(text []byte) float64 {
		fields := strings.Fields(string(text))
		if len(fields) == 0 {
			return -1
		}
		value, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return -1
		}
		return value
	}
	return available >= 32768 && first(mmStat) >= 8<<20 && first(uptime) > 60
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
