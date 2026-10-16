package osupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"NanoKVM-Server/internal/apkrun"
)

// Update notices come from a light, periodic look at the NanoKVM repository
// only. The check refreshes the NanoKVM index (a few KiB) into a cache of its
// own in /run and compares it with the installed packages. The Alpine indexes,
// several MiB that every apk run then parses for 15-18 s of CPU, are left to
// the Software page and its schedule.
//
// A NanoKVM repository is an untagged line of /etc/apk/repositories whose URL
// contains "nanokvm" in any case: the official repository wherever it is
// hosted, and not the C906 overlay.
const (
	noticeFirstDelay = 2 * time.Minute
	noticeTick       = 10 * time.Minute
	noticeInterval   = 24 * time.Hour
	noticeRetry      = time.Hour
	noticeTimeout    = time.Minute
	// One small index and the installed database, far below the default
	// repositories' 32 MiB.
	noticeMemoryKiB = 12 * 1024
)

var (
	noticeDisabledPath = "/etc/kvm/update-check.disabled"
	noticeCacheDir     = "/run/nanokvm-update-check"
	repositoriesPath   = "/etc/apk/repositories"
	installedDBPath    = "/lib/apk/db/installed"
	noticeNow          = time.Now
	noticeApk          = runNoticeAPK

	noticeChecking atomic.Bool
)

// UpdateNotice is what the menu shows: the NanoKVM packages with a newer
// version in the repository, as of the last check.
type UpdateNotice struct {
	Enabled bool `json:"enabled"`
	// never, checking, ok, failed or unavailable (no NanoKVM repository).
	State string `json:"state"`
	// CheckedAt is the last successful refresh of the index, AttemptAt the
	// last try; both Unix seconds.
	CheckedAt int64            `json:"checkedAt,omitempty"`
	AttemptAt int64            `json:"attemptAt,omitempty"`
	Message   string           `json:"message,omitempty"`
	Updates   []SoftwareUpdate `json:"updates"`
}

func noticePath() string { return apkStateDir + "/notice.json" }

func NoticeEnabled() bool {
	_, err := os.Stat(noticeDisabledPath)
	return errors.Is(err, os.ErrNotExist)
}

func SetNoticeEnabled(enabled bool) error {
	if enabled {
		if err := os.Remove(noticeDisabledPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeAtomic(noticeDisabledPath, nil, 0644)
}

func GetNotice() UpdateNotice {
	n := UpdateNotice{State: "never"}
	if b, err := os.ReadFile(noticePath()); err == nil {
		_ = json.Unmarshal(b, &n)
	}
	if n.Updates == nil {
		n.Updates = []SoftwareUpdate{}
	}
	n.Enabled = NoticeEnabled()
	if noticeChecking.Load() {
		n.State = "checking"
	}
	return n
}

func saveNotice(n UpdateNotice) error {
	if err := os.MkdirAll(apkStateDir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return writeAtomic(noticePath(), b, 0600)
}

func nanokvmRepositories() ([]string, error) {
	data, err := os.ReadFile(repositoriesPath)
	if err != nil {
		return nil, err
	}
	var repos []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "@") {
			continue
		}
		if strings.Contains(strings.ToLower(line), "nanokvm") {
			repos = append(repos, line)
		}
	}
	return repos, nil
}

// The NanoKVM repositories alone, with their own cache, in the --opt=value
// form so that apkrun sees the applet as the first plain argument.
func noticeArgs(repos []string, applet ...string) []string {
	args := []string{"--repositories-file=/dev/null", "--cache-dir=" + noticeCacheDir}
	for _, repo := range repos {
		args = append(args, "--repository="+repo)
	}
	return append(args, applet...)
}

func runNoticeAPK(ctx context.Context, args ...string) (string, error) {
	cmd := apkrun.Command(ctx, apkExecutable, args...)
	cmd.LockWait, cmd.Timeout, cmd.MemoryKiB = 5*time.Second, noticeTimeout, noticeMemoryKiB
	b, err := cmd.CombinedOutput()
	if len(b) > softwareLogLimit {
		b = b[len(b)-softwareLogLimit:]
	}
	return string(b), err
}

// The server and detached update helpers must serialize the whole notice
// transaction, not just each apk child command. Offline post-upgrade checks
// wait for the current check so their newer installed state wins.
func lockNotice(ctx context.Context, wait bool) (*os.File, error) {
	if err := os.MkdirAll(apkStateDir, 0700); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(filepath.Join(apkStateDir, "notice.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), "notice-lock")
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		lock.Close()
		return nil, errors.New("unsafe notice lock")
	}
	for {
		if err := ctx.Err(); err != nil {
			lock.Close()
			return nil, err
		}
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			lock.Close()
			return nil, err
		}
		if !wait {
			lock.Close()
			return nil, nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			lock.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// CheckUpdates refreshes the NanoKVM index when network is true, then
// compares it with the installed packages. Without network it only compares,
// which is how a finished package operation clears the notice. A check that
// is already running makes a second network check return at once; an offline
// comparison waits so a completed package transaction cannot leave stale notices.
func CheckUpdates(ctx context.Context, network bool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*noticeTimeout+10*time.Second)
	defer cancel()
	lock, err := lockNotice(ctx, !network)
	if err != nil || lock == nil {
		return err
	}
	defer lock.Close()
	if network {
		noticeChecking.Store(true)
		defer noticeChecking.Store(false)
	}
	n := GetNotice()
	now := noticeNow().Unix()
	if network {
		n.AttemptAt = now
	} else if n.CheckedAt == 0 {
		return nil // nothing fetched yet to compare with
	}
	repos, err := nanokvmRepositories()
	if err == nil && len(repos) == 0 {
		n.State, n.Message, n.Updates = "unavailable", "no NanoKVM repository is configured", nil
		return saveNotice(n)
	}
	if err == nil {
		err = os.MkdirAll(noticeCacheDir, 0755)
	}
	if err == nil && network {
		if out, e := noticeApk(ctx, noticeArgs(repos, "update")...); e != nil {
			err = fmt.Errorf("index refresh failed: %s", strings.TrimSpace(out))
		} else {
			n.CheckedAt = now
		}
	}
	if err == nil {
		var out string
		if out, err = noticeApk(ctx, noticeArgs(repos, "--no-network", "version", "-l", "<")...); err != nil {
			err = fmt.Errorf("version check failed: %s", strings.TrimSpace(out))
		} else {
			n.Updates = parseSoftwareUpdates(out)
		}
	}
	if err != nil {
		// Keep the last known updates: they are still the best information.
		n.State, n.Message = "failed", err.Error()
	} else {
		n.State, n.Message = "ok", ""
	}
	if saveErr := saveNotice(n); saveErr != nil {
		return saveErr
	}
	return err
}

// noticeDue: enabled, and either never checked successfully or a day old,
// with failed attempts retried hourly (the network may come up after boot).
func noticeDue(n UpdateNotice, now time.Time) bool {
	if !n.Enabled {
		return false
	}
	if n.AttemptAt != 0 && now.Sub(time.Unix(n.AttemptAt, 0)) < noticeRetry {
		return false
	}
	return n.State == "failed" || n.CheckedAt == 0 || now.Sub(time.Unix(n.CheckedAt, 0)) >= noticeInterval
}

// RunNoticeSchedule checks for updates a while after the server starts and
// then once a day. The state is in /run, so a server restart does not repeat
// a check and a reboot does.
func RunNoticeSchedule(ctx context.Context) {
	timer := time.NewTimer(noticeFirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := os.Stat("/etc/alpine-release"); err == nil && noticeDue(GetNotice(), noticeNow()) {
			_ = CheckUpdates(ctx, true)
		}
		timer.Reset(noticeTick)
	}
}

// Component is one installed NanoKVM OS package; Available is set when the
// last check found a newer version.
type Component struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Available string `json:"available,omitempty"`
}

type Components struct {
	Packages []Component `json:"packages"`
	Alpine   string      `json:"alpine"`
	// Kernel runs now; BootKernel starts at the next boot.
	Kernel     string `json:"kernel"`
	BootKernel string `json:"bootKernel"`
}

var componentOrder = []string{"nanokvm", "nanokvm-release", "nanokvm-app", "nanokvm-web", "nanokvm-base",
	"nanokvm-kernel-sg2002", "nanokvm-kmod-sg2002", "nanokvm-firmware-sg2002", "nanokvm-keys"}

var componentCache struct {
	sync.Mutex
	mtime time.Time
	size  int64
	list  []Component
}

// installedComponents reads apk's installed database directly: an apk run
// would load the indexes. The result is cached until the file changes.
func installedComponents() []Component {
	info, err := os.Stat(installedDBPath)
	if err != nil {
		return []Component{}
	}
	componentCache.Lock()
	defer componentCache.Unlock()
	if componentCache.list != nil && info.ModTime().Equal(componentCache.mtime) && info.Size() == componentCache.size {
		return componentCache.list
	}
	data, err := os.ReadFile(installedDBPath)
	if err != nil {
		return []Component{}
	}
	list := []Component{}
	name := ""
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case line == "":
			name = ""
		case strings.HasPrefix(line, "P:"):
			name = line[2:]
		case strings.HasPrefix(line, "V:") && (name == "nanokvm" || strings.HasPrefix(name, "nanokvm-")):
			list = append(list, Component{Name: name, Installed: line[2:]})
		}
	}
	rank := func(name string) int {
		for i, known := range componentOrder {
			if known == name {
				return i
			}
		}
		return len(componentOrder)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if ri, rj := rank(list[i].Name), rank(list[j].Name); ri != rj {
			return ri < rj
		}
		return list[i].Name < list[j].Name
	})
	componentCache.mtime, componentCache.size, componentCache.list = info.ModTime(), info.Size(), list
	return list
}

var (
	alpineReleasePath = "/etc/alpine-release"
	kernelReleasePath = "/proc/sys/kernel/osrelease"
	bootReleasePath   = "/boot/kernel.release"
)

func readTrimmed(path string) string {
	b, _ := os.ReadFile(filepath.Clean(path))
	return strings.TrimSpace(string(b))
}

func GetComponents() Components {
	available := map[string]string{}
	for _, u := range GetNotice().Updates {
		available[u.Name] = u.Available
	}
	installed := installedComponents()
	packages := make([]Component, 0, len(installed))
	for _, c := range installed {
		c.Available = available[c.Name]
		packages = append(packages, c)
	}
	return Components{
		Packages:   packages,
		Alpine:     readTrimmed(alpineReleasePath),
		Kernel:     readTrimmed(kernelReleasePath),
		BootKernel: readTrimmed(bootReleasePath),
	}
}
