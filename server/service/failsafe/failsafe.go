// Package failsafe keeps management reachable after repeated native video
// crashes. Inspired by IronKVM a063068b; adapted to our init and media stack.
package failsafe

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxRecordBytes = 16480
const threshold = 3
const windowSeconds = 600

type State struct {
	Active   bool `json:"active"`
	Crashes  int  `json:"crashes"`
	Retrying bool `json:"retrying"`
}

type Guard struct {
	mu      sync.Mutex
	dir     string
	state   State
	restart func() error
}

var current = &Guard{}

func directory() string {
	if path := os.Getenv("NANOKVM_RECOVERY_DIR"); path != "" {
		return path
	}
	return "/run/nanokvm/recovery"
}

func uptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return -1
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return -1
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return -1
	}
	return int64(value)
}

// Init must precede the first native video call. The latch is per boot; normal
// service restarts keep it, an administrator's retry or a reboot clears it.
func Init(restart func() error) (bool, error) {
	g, err := load(directory(), uptime(), restart)
	if err != nil {
		return false, err
	}
	current = g
	crash, err := os.OpenFile(filepath.Join(g.dir, "crash.txt"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return g.state.Active, err
	}
	defer crash.Close()
	err = debug.SetCrashOutput(crash, debug.CrashOptions{})
	return g.state.Active, err
}

func load(dir string, now int64, restart func() error) (*Guard, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	g := &Guard{dir: dir, restart: restart}
	for i := 1; i <= 8; i++ {
		path := filepath.Join(dir, fmt.Sprintf("exit-%d.txt", i))
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxRecordBytes {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil && recentVideoCrash(string(data), now) {
			g.state.Crashes++
		}
	}
	_, err := os.Stat(filepath.Join(dir, "active"))
	g.state.Active = err == nil || g.state.Crashes >= threshold
	if g.state.Active {
		if err := os.WriteFile(filepath.Join(dir, "active"), []byte("1\n"), 0600); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func recentVideoCrash(record string, now int64) bool {
	header, report, ok := strings.Cut(record, "\n")
	if !ok {
		return false
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return false
	}
	at, e1 := strconv.ParseInt(fields[0], 10, 64)
	status, e2 := strconv.Atoi(fields[1])
	if e1 != nil || e2 != nil || at < 0 || now < at || now-at > windowSeconds || status == 0 {
		return false
	}
	if fields[2] != "initializing" && fields[2] != "ready" {
		return false
	}
	if strings.TrimSpace(report) == "" {
		// A fatal native pthread signal may never reach Go's crash writer.
		// OOM (137) and intentional TERM/QUIT are deliberately excluded.
		return status == 139 || status == 134 || status == 135 || status == 132
	}
	// Only the failing goroutine is evidence. Other goroutines commonly sit
	// inside libkvm even when an unrelated component panics.
	if start := strings.Index(report, "\ngoroutine "); start >= 0 {
		if end := strings.Index(report[start+1:], "\ngoroutine "); end >= 0 {
			report = report[:start+1+end]
		}
	}
	for _, token := range []string{"_Cfunc_kvmv_", "_Cfunc_nkosRead", "_Cfunc_nk_capture_", "_Cfunc_set_h264_", "_Cfunc_set_h265_", "_Cfunc_get_h265_", "_Cfunc_get_mjpeg_", "_Cfunc_set_mjpeg_", "_Cfunc_set_frame_detact", "_Cfunc_set_capture_fps", "common.(*KvmVision)."} {
		if strings.Contains(report, token) {
			return true
		}
	}
	return false
}

func Status() State            { return current.status() }
func (g *Guard) status() State { g.mu.Lock(); defer g.mu.Unlock(); return g.state }

func VideoPhase(phase string) error {
	return os.WriteFile(filepath.Join(current.dir, "video-phase"), []byte(phase), 0600)
}

func Retry() error { return current.retry() }
func (g *Guard) retry() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.state.Active || g.state.Retrying || g.restart == nil {
		return fmt.Errorf("video retry is unavailable")
	}
	g.state.Retrying = true
	go func() {
		// Let the HTTP acknowledgement reach the browser before exec.
		time.Sleep(time.Second)
		g.mu.Lock()
		defer g.mu.Unlock()
		err := g.clear()
		if err == nil {
			err = g.restart()
		}
		if err != nil {
			log.Errorf("video recovery retry failed: %v", err)
		}
		// Exec only returns on failure. Preserve recovery and allow another
		// attempt, rather than leaving the UI claiming a restart is pending.
		_ = os.WriteFile(filepath.Join(g.dir, "active"), []byte("1\n"), 0600)
		g.state.Retrying = false
	}()
	return nil
}

func (g *Guard) clear() error {
	for i := 0; i <= 8; i++ {
		name := "active"
		if i > 0 {
			name = fmt.Sprintf("exit-%d.txt", i)
		}
		if err := os.Remove(filepath.Join(g.dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

type Crash struct {
	Uptime int64  `json:"uptime"`
	Status int    `json:"status"`
	Phase  string `json:"phase"`
	Report string `json:"report"`
}

// Crashes is an administrator-only diagnostic, not part of live status.
func Crashes() []Crash { return current.crashes() }
func (g *Guard) crashes() []Crash {
	g.mu.Lock()
	defer g.mu.Unlock()
	records := make([]Crash, 0, 8)
	for i := 1; i <= 8; i++ {
		f, err := os.Open(filepath.Join(g.dir, fmt.Sprintf("exit-%d.txt", i)))
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxRecordBytes))
		f.Close()
		if err != nil {
			continue
		}
		header, report, ok := strings.Cut(string(data), "\n")
		if !ok {
			continue
		}
		var record Crash
		if _, err := fmt.Sscanf(header, "%d %d %s", &record.Uptime, &record.Status, &record.Phase); err != nil {
			continue
		}
		record.Report = report
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Uptime > records[j].Uptime })
	return records
}
