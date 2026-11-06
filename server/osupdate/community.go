package osupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"NanoKVM-Server/internal/apkrun"
)

// From 3.2 Alpine's community repository is off by default: the image takes
// nothing from it (exfatprogs and i2c-tools are NanoKVM packages), and every
// apk run would parse its index. The Software page switches it on, and the
// Tailscale and NetBird installs do so themselves.

var (
	communityTmpDir = "/run"

	// updateLock is Lock, which tests replace.
	updateLock = Lock
)

// errCommunityUnknown means the needed packages cannot be determined now,
// usually because an index is missing and the device is offline.
var errCommunityUnknown = errors.New("cannot determine which packages need the community repository")

type CommunityStatus struct {
	Enabled bool `json:"enabled"`
	// Available is false when no Alpine main repository is listed, so no
	// community line can be derived (a custom setup); the switch is then off.
	Available bool `json:"available"`
}

// CommunityChange is the result of a request to switch: Needed lists the
// installed packages that keep the repository on.
type CommunityChange struct {
	Enabled bool     `json:"enabled"`
	Needed  []string `json:"needed,omitempty"`
}

func activeRepositoryLines(repositories string) []string {
	var lines []string
	for _, line := range strings.Split(repositories, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}

// communityURL derives the community repository from the untagged Alpine main
// one, so a device that uses another mirror keeps it.
func communityURL(repositories string) string {
	for _, line := range activeRepositoryLines(repositories) {
		if strings.HasPrefix(line, "@") {
			continue
		}
		url := strings.TrimRight(line, "/")
		if strings.HasSuffix(url, "/main") && strings.Contains(url, "alpine") {
			return strings.TrimSuffix(url, "/main") + "/community"
		}
	}
	return ""
}

func hasRepository(repositories, url string) bool {
	for _, line := range activeRepositoryLines(repositories) {
		if strings.TrimRight(line, "/") == url {
			return true
		}
	}
	return false
}

// withCommunity returns the repositories with the community line removed, or
// placed right after the main line.
func withCommunity(repositories, url string, enabled bool) string {
	var out []string
	for _, line := range strings.Split(repositories, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.TrimRight(trimmed, "/") == url {
			continue
		}
		out = append(out, line)
		if enabled && !strings.HasPrefix(trimmed, "@") && strings.TrimRight(trimmed, "/") == strings.TrimSuffix(url, "/community")+"/main" {
			out = append(out, url)
		}
	}
	return strings.Join(out, "\n")
}

func GetCommunity() (CommunityStatus, error) {
	data, err := os.ReadFile(repositoriesPath)
	if err != nil {
		return CommunityStatus{}, err
	}
	url := communityURL(string(data))
	return CommunityStatus{Enabled: url != "" && hasRepository(string(data), url), Available: url != ""}, nil
}

// orphaned lists the installed packages that none of the repositories in the
// given list provides. apk reports a missing index only as a warning, and then
// calls every package orphaned: any output on stderr is errCommunityUnknown.
func orphaned(ctx context.Context, repositories string) (map[string]bool, error) {
	f, err := os.CreateTemp(communityTmpDir, "apk-repositories.")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(repositories + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd := apkrun.Command(ctx, apkExecutable, "--repositories-file", f.Name(), "list", "--orphaned")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.LockWait, cmd.Timeout = softwareLockWait, time.Minute
	if err = cmd.Run(); err != nil || strings.TrimSpace(stderr.String()) != "" {
		return nil, fmt.Errorf("%w: %s", errCommunityUnknown, strings.TrimSpace(stderr.String()))
	}
	names := map[string]bool{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		// NAME-VERSION-rN: the version and the release are the last two parts.
		parts := strings.Split(fields[0], "-")
		if len(parts) < 3 {
			return nil, fmt.Errorf("%w: unexpected apk list line %q", errCommunityUnknown, line)
		}
		names[strings.Join(parts[:len(parts)-2], "-")] = true
	}
	return names, nil
}

// communityNeeded lists the installed packages that only the community
// repository provides.
func communityNeeded(ctx context.Context, repositories, url string) ([]string, error) {
	without, err := orphaned(ctx, withCommunity(repositories, url, false))
	if err != nil {
		return nil, err
	}
	with, err := orphaned(ctx, withCommunity(repositories, url, true))
	if err != nil {
		return nil, err
	}
	var needed []string
	for name := range without {
		if !with[name] {
			needed = append(needed, name)
		}
	}
	sort.Strings(needed)
	return needed, nil
}

// lockCommunityDB keeps the installed package set stable across both orphan
// queries and the repository decision. Callers already hold the update lock;
// apk's read-only list command does not take this database lock.
func lockCommunityDB() (*os.File, error) {
	fd, err := syscall.Open(apkDBLockPath, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(fd)
		return nil, errAPKDBBusy
	}
	return os.NewFile(uintptr(fd), apkDBLockPath), nil
}

// writeCommunity switches the line while the caller holds lockCommunityDB.
func writeCommunity(url string, enabled bool) error {
	data, err := os.ReadFile(repositoriesPath)
	if err != nil {
		return err
	}
	if hasRepository(string(data), url) == enabled {
		return nil
	}
	return writeAtomic(repositoriesPath, []byte(withCommunity(string(data), url, enabled)), 0644)
}

func lockedCommunityURL() (string, error) {
	data, err := os.ReadFile(repositoriesPath)
	if err != nil {
		return "", err
	}
	url := communityURL(string(data))
	if url == "" {
		return "", errors.New("no Alpine main repository is listed; the community repository cannot be derived")
	}
	return url, nil
}

// SetCommunity switches the repository on the Software page. Switching it off
// is refused while installed packages need it; Needed lists them.
func SetCommunity(enabled bool) (CommunityChange, error) {
	lock, err := updateLock()
	if err != nil {
		return CommunityChange{}, err
	}
	defer lock.Close()
	dbLock, err := lockCommunityDB()
	if err != nil {
		return CommunityChange{}, err
	}
	defer dbLock.Close()
	url, err := lockedCommunityURL()
	if err != nil {
		return CommunityChange{}, err
	}
	if !enabled {
		data, err := os.ReadFile(repositoriesPath)
		if err != nil {
			return CommunityChange{}, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		needed, err := communityNeeded(ctx, string(data), url)
		if err != nil {
			return CommunityChange{}, err
		}
		if len(needed) > 0 {
			return CommunityChange{Enabled: true, Needed: needed}, nil
		}
	}
	if err = writeCommunity(url, enabled); err != nil {
		return CommunityChange{}, err
	}
	return CommunityChange{Enabled: enabled}, nil
}

// EnableCommunityLocked switches the repository on for an install of
// community packages. The caller holds Lock().
func EnableCommunityLocked() error {
	dbLock, err := lockCommunityDB()
	if err != nil {
		return err
	}
	defer dbLock.Close()
	url, err := lockedCommunityURL()
	if err != nil {
		return err
	}
	return writeCommunity(url, true)
}
