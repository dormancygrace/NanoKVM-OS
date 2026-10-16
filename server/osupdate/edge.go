package osupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

// Releases up to 3.1 listed Alpine edge/community, tagged @edgecommunity, for
// Tailscale and NetBird; the stable branch has both. Every apk run parsed the
// 2 MB edge index: 30% of an `apk version` and 10 MB of memory on the device.
//
// The line cannot simply go: a world entry with a tag that no repository has
// stops every apk transaction. So the world first keeps each package that came
// from edge at its installed version or newer (no downgrade to the older stable
// build; stable takes over once it catches up), and the line goes after that.
// This runs outside any apk transaction, under apk's own database lock: every
// apk transaction holds it, the commit hook and nkos-update's detached runs
// included. Read-only queries do not take it; they read either the old or the
// new file, as both are replaced by rename.
const edgeTag = "@edgecommunity"

var (
	worldPath     = "/etc/apk/world"
	apkDBLockPath = "/lib/apk/db/lock"

	edgeFirstDelay = 30 * time.Second
	edgeRetry      = 10 * time.Minute
)

var edgeWorldEntry = regexp.MustCompile(`^([a-z0-9][a-z0-9+_.-]{0,127})` + edgeTag + `(.*)$`)

// errEdgeBusy means apk holds its database; the migration retries later.
var errEdgeBusy = errors.New("apk database is in use")

func hasEdgeRepository(repositories string) bool {
	for _, line := range strings.Split(repositories, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == edgeTag {
			return true
		}
	}
	return false
}

// untagWorld rewrites the world entries of the edge tag; done is false when an
// entry has a form this does not know, which leaves both files unchanged.
func untagWorld(world string, installed map[string]string) (string, bool) {
	lines := strings.Split(world, "\n")
	for i, line := range lines {
		entry := strings.TrimSpace(line)
		if !strings.Contains(entry, edgeTag) {
			continue
		}
		m := edgeWorldEntry.FindStringSubmatch(entry)
		if m == nil {
			return world, false
		}
		name, constraint := m[1], m[2]
		switch {
		case constraint != "":
			lines[i] = name + constraint
		case installed[name] != "":
			lines[i] = name + ">=" + installed[name]
		default:
			lines[i] = name
		}
	}
	return strings.Join(lines, "\n"), true
}

func dropEdgeRepository(repositories string) string {
	var kept []string
	for _, line := range strings.Split(repositories, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == edgeTag {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func installedVersions() (map[string]string, error) {
	data, err := os.ReadFile(installedDBPath)
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	name := ""
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case line == "":
			name = ""
		case strings.HasPrefix(line, "P:"):
			name = line[2:]
		case strings.HasPrefix(line, "V:") && name != "":
			versions[name] = line[2:]
		}
	}
	return versions, nil
}

// MigrateEdgeRepository does the move once; it returns errEdgeBusy while apk
// runs, and nil when there is nothing (left) to do.
func MigrateEdgeRepository() error {
	repositories, err := os.ReadFile(repositoriesPath)
	if err != nil || !hasEdgeRepository(string(repositories)) {
		return err
	}
	fd, err := syscall.Open(apkDBLockPath, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errEdgeBusy
	}
	// Read again under the lock: an apk run may have changed both files.
	if repositories, err = os.ReadFile(repositoriesPath); err != nil || !hasEdgeRepository(string(repositories)) {
		return err
	}
	world, err := os.ReadFile(worldPath)
	if err != nil {
		return err
	}
	installed, err := installedVersions()
	if err != nil {
		return err
	}
	next, ok := untagWorld(string(world), installed)
	if !ok {
		return fmt.Errorf("%s has an %s entry of an unknown form; the edge repository stays", worldPath, edgeTag)
	}
	// The world first: with only it done, the tag line is still there and the
	// untagged entries are valid anyway.
	if next != string(world) {
		if err = writeAtomic(worldPath, []byte(next), 0644); err != nil {
			return err
		}
	}
	if err = writeAtomic(repositoriesPath, []byte(dropEdgeRepository(string(repositories))), 0644); err != nil {
		return err
	}
	log.Infof("removed the Alpine edge repository; world: %s", strings.Join(strings.Fields(next), " "))
	return nil
}

// RunEdgeMigration tries the move shortly after the server starts and retries
// while apk is busy or a step fails, until it is done.
func RunEdgeMigration(ctx context.Context) {
	delay := edgeFirstDelay
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		err := MigrateEdgeRepository()
		if err == nil {
			return
		}
		if !errors.Is(err, errEdgeBusy) {
			log.Warnf("Alpine edge repository: %s", err)
		}
		delay = edgeRetry
	}
}
