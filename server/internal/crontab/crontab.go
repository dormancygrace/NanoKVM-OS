// Package crontab edits root's crontab for the parts of the server that
// schedule jobs: the APK index refresh (osupdate) and Scheduled tasks (vm).
// Each rewrites the whole file, so they share one lock. The file is
// persisted under /etc/kvm/cron by nanokvm-base; BusyBox crond runs each line
// with /bin/sh -c and gives % no meaning.
package crontab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Mu serializes every read-modify-write of the file.
var Mu sync.Mutex

// Test seams.
var (
	Path = "/etc/crontabs/root"
	Exec = func(input []byte, name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdin = bytes.NewReader(input)
		return cmd.CombinedOutput()
	}
)

// Read returns the crontab; a missing one is empty. The caller holds Mu.
func Read() ([]byte, error) {
	data, err := os.ReadFile(Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// Install replaces the crontab through crontab(1), which also tells the
// running crond. The caller holds Mu.
func Install(data []byte) error {
	if output, err := Exec(data, "crontab", "-"); err != nil {
		return fmt.Errorf("crontab: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// EnsureDaemon enables crond at boot and starts it.
func EnsureDaemon() error {
	if _, err := Exec(nil, "rc-update", "add", "crond", "default"); err != nil {
		return fmt.Errorf("enable cron: %w", err)
	}
	if _, err := Exec(nil, "rc-service", "crond", "status"); err != nil {
		if _, err := Exec(nil, "rc-service", "crond", "start"); err != nil {
			return fmt.Errorf("start cron: %w", err)
		}
	}
	return nil
}
