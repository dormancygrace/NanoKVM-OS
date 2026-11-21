package osupdate

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// Alpine's community repository is listed: LLDP and UPS (lldpd, nut) come from
// it. The image lists it, and nanokvm-base's post-upgrade script adds it on a
// device updated from a release without it, by the same rules as here. An
// install of community packages makes sure of it again, for a list that lost
// the line.

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

// hasCommunity reports whether an untagged line is a community repository of
// the Alpine branch of url (".../v3.24/community"), from any mirror and over
// either scheme: a second one would only make apk parse the same index twice.
func hasCommunity(repositories, url string) bool {
	branch := url[max(strings.LastIndex(strings.TrimSuffix(url, "/community"), "/"), 0):]
	for _, line := range activeRepositoryLines(repositories) {
		if !strings.HasPrefix(line, "@") && strings.HasSuffix(strings.TrimRight(line, "/"), branch) {
			return true
		}
	}
	return false
}

// withCommunity returns the repositories with the community line placed right
// after the first untagged main line.
func withCommunity(repositories, url string) string {
	main := strings.TrimSuffix(url, "/community") + "/main"
	var out []string
	added := false
	for _, line := range strings.Split(repositories, "\n") {
		out = append(out, line)
		trimmed := strings.TrimSpace(line)
		if !added && !strings.HasPrefix(trimmed, "@") && strings.TrimRight(trimmed, "/") == main {
			out = append(out, url)
			added = true
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

// writeCommunity adds the line, unless the list has one, while the caller
// holds lockCommunityDB.
func writeCommunity(url string) error {
	data, err := os.ReadFile(repositoriesPath)
	if err != nil {
		return err
	}
	if hasCommunity(string(data), url) {
		return nil
	}
	return writeRepositories([]byte(withCommunity(string(data), url)))
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

// EnableCommunityLocked lists the repository for an install of
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
	return writeCommunity(url)
}
