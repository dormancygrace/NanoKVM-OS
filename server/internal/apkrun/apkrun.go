// Package apkrun is the one way NanoKVM-Server and its helpers run apk.
//
// Measured on the SG2002 in the 4K video memory mode (about 104 MiB for
// Linux, zram swap of half that): an apk run that loads the repository indexes
// (query --from=repositories, search, version, info --verbose, update, add,
// del, upgrade) peaks at 33-36 MiB of anonymous memory with the default
// repositories, plus about 4 MiB of shared file pages, and needs 15-18 s of
// CPU. Two of them at once do not fit, and the OOM killer then chose one of
// them or the web server. Therefore every apk started through this package
//
//   - holds an exclusive flock on LockPath while it runs, so apk runs from the
//     server, the detached update helper and the server after a restart queue
//     behind each other instead of overlapping;
//   - is refused before it starts when even swapping every other process out
//     to zram could not make room for it (see checkMemory);
//   - gets oom_score_adj 1000, so the kernel kills it before anything else;
//   - runs at nice 19 when it only reads the indexes, so streaming and HTTP
//     requests keep the CPU.
//
// Package transactions keep the caller's priority: package scripts and the
// commit hook start and restart services, which would inherit nice 19.
// Queries of the installed database only (info -e) need about 7 MiB and well
// under a second; they skip the lock, so status pages stay responsive while a
// long upgrade runs.
//
// The lock is taken on a descriptor that is closed on exec. apk's commit hook
// (nkos-apply-updates) runs while the lock is held and must not take it.
package apkrun

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"NanoKVM-Server/internal/oomscore"

	"golang.org/x/sys/unix"
)

// LockPath is shared by every process that runs apk through this package.
var LockPath = "/run/nanokvm-apk.lock"

var (
	memInfoPath = "/proc/meminfo"
	lockPoll    = 200 * time.Millisecond
)

// requiredKiB is the smallest measured anonymous peak of an index-loading apk
// run with the default repositories (Alpine main and community and the
// NanoKVM repository): 36.8 MiB maximum RSS of which about 4 MiB are shared
// file pages. checkMemory compares it with an upper bound of the memory apk
// can get, so a refusal means the run would certainly have pushed the system
// into the OOM killer, not that it might.
const requiredKiB = 32 * 1024

// ErrBusy means the wait for another apk run ended before that run finished.
var ErrBusy = errors.New("another package operation is still running; try again when it has finished")

// LowMemoryError means apk was not started because it could not fit in memory.
type LowMemoryError struct {
	AvailableKiB, RequiredKiB int64
}

func (e *LowMemoryError) Error() string {
	return fmt.Sprintf("not enough free memory to run APK safely: about %d MiB available, %d MiB needed; "+
		"close remote sessions or stop unused add-ons, or reboot, and try again", e.AvailableKiB/1024, e.RequiredKiB/1024)
}

// notStartedError marks an error that kept apk from starting.
type notStartedError struct{ err error }

func (e *notStartedError) Error() string { return e.err.Error() }
func (e *notStartedError) Unwrap() error { return e.err }

// NotStarted reports whether err means apk never ran: the lock wait ended,
// memory was insufficient or the lock file was unusable.
func NotStarted(err error) bool {
	var target *notStartedError
	return errors.As(err, &target)
}

type class int

const (
	installedOnly class = iota // reads the installed database only
	indexQuery                 // loads the repository indexes, runs no scripts
	transaction                // changes packages, runs package scripts and hooks
)

// classify looks at the applet, the first argument that is not an option, and
// treats anything it does not know as an index query.
func classify(args []string) class {
	applet := ""
	options := map[string]bool{}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			options[arg] = true
		} else if applet == "" {
			applet = arg
		}
	}
	switch applet {
	case "info":
		if options["-e"] || options["--installed"] {
			return installedOnly
		}
	case "add", "del", "upgrade", "fix":
		if !options["-s"] && !options["--simulate"] {
			return transaction
		}
	}
	return indexQuery
}

// Cmd describes one apk run, like exec.Cmd.
type Cmd struct {
	Path string
	Args []string // without Path

	Stdout, Stderr io.Writer

	// LockWait bounds the wait for another apk run. Zero waits as long as the
	// context allows.
	LockWait time.Duration
	// Timeout bounds the apk run itself, counted once it holds the lock. Zero
	// leaves only the context.
	Timeout time.Duration

	ctx context.Context
}

// Command returns a Cmd that runs path (the apk executable) with args. The
// context bounds the whole call, including the wait for the lock.
func Command(ctx context.Context, path string, args ...string) *Cmd {
	return &Cmd{Path: path, Args: args, ctx: ctx}
}

// CombinedOutput runs apk and returns its standard output and error. When apk
// is not started, the output is the reason, so callers that report apk's
// output explain the failure without special cases.
func (c *Cmd) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil || c.Stderr != nil {
		return nil, errors.New("apkrun: Stdout or Stderr already set")
	}
	var output bytes.Buffer
	c.Stdout, c.Stderr = &output, &output
	err := c.Run()
	if NotStarted(err) {
		return []byte(err.Error()), err
	}
	return output.Bytes(), err
}

// Run runs apk. When apk is not started, the reason is also written to
// Stderr, which is usually an operation log.
func (c *Cmd) Run() error {
	err := c.run()
	if NotStarted(err) && c.Stderr != nil {
		fmt.Fprintln(c.Stderr, err)
	}
	return err
}

func (c *Cmd) run() error {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	kind := classify(c.Args)
	if kind != installedOnly {
		lock, err := acquire(ctx, c.LockWait)
		if err != nil {
			return &notStartedError{err}
		}
		defer lock.Close()
		if err = checkMemory(); err != nil {
			return &notStartedError{err}
		}
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Stdout, cmd.Stderr = c.Stdout, c.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// Set on the running child instead of through a wrapper: apk needs far
	// longer than this to load anything, and nothing else can fail here.
	pid := cmd.Process.Pid
	_ = oomscore.Set(pid, oomscore.Expendable)
	if kind == indexQuery {
		_ = unix.Setpriority(unix.PRIO_PROCESS, pid, 19)
	}
	return cmd.Wait()
}

// acquire takes the exclusive lock, polling so that both the context and the
// wait limit can end the wait.
func acquire(ctx context.Context, wait time.Duration) (*os.File, error) {
	fd, err := syscall.Open(LockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot open APK lock: %w", err)
	}
	lock := os.NewFile(uintptr(fd), LockPath)
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		lock.Close()
		return nil, errors.New("unsafe APK lock file")
	}
	var expired <-chan time.Time
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		expired = timer.C
	}
	ticker := time.NewTicker(lockPoll)
	defer ticker.Stop()
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			lock.Close()
			return nil, fmt.Errorf("cannot lock APK: %w", err)
		}
		select {
		case <-ctx.Done():
			lock.Close()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, ErrBusy
			}
			return nil, ctx.Err()
		case <-expired:
			lock.Close()
			return nil, ErrBusy
		case <-ticker.C:
		}
	}
}

// checkMemory refuses an index-loading run that cannot fit. MemAvailable is
// what the kernel can hand out without swapping. On top of it, zram can take
// at most the anonymous pages of other processes (AnonPages, apk is not yet
// running) and stores them at about 2:1 (measured mem_used_total against
// orig_data_size), so swapping frees at most half of min(SwapFree, AnonPages).
// An unreadable meminfo blocks nothing.
func checkMemory() error {
	info, err := readMemInfo(memInfoPath)
	if err != nil {
		return nil
	}
	available, ok := info["MemAvailable"]
	if !ok {
		return nil
	}
	budget := available + min(info["SwapFree"], info["AnonPages"])/2
	if budget < requiredKiB {
		return &LowMemoryError{AvailableKiB: budget, RequiredKiB: requiredKiB}
	}
	return nil
}

// readMemInfo returns the kB values of /proc/meminfo.
func readMemInfo(path string) (map[string]int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]int64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		name, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if value, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
			values[name] = value
		}
	}
	return values, scanner.Err()
}
