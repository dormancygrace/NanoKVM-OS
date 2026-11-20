package osupdate

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// Alpine's community repository is on: the base set takes packages from it
// (the image lists it; nanokvm-base adds it on devices updated from an earlier
// release). An install of community packages still makes sure of it, for a
// list that lost the line.

// updateLock is Lock, which tests replace.
var updateLock = Lock

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

// lockCommunityDB keeps apk off the repository list while it changes. Callers
// already hold the update lock.
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
