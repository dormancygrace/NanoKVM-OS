package apkpkg

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"NanoKVM-Server/internal/apkrun"
	"NanoKVM-Server/osupdate"
)

var packageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.-]{0,127}$`)

// Run executes one APK package operation while holding the same system-wide
// lock as firmware and generic Software updates.
func Run(action, name string) error {
	lock, err := osupdate.Lock()
	if err != nil {
		return err
	}
	defer lock.Close()

	return osupdate.RunSoftware(action, name)
}

// Install installs packages together from the configured repositories, after
// refreshing the indexes, under the same lock as other package operations.
func Install(names ...string) error {
	if len(names) == 0 {
		return errors.New("no APK package to install")
	}
	for _, name := range names {
		if !packageName.MatchString(name) {
			return errors.New("invalid APK package name")
		}
	}
	lock, err := osupdate.Lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if output, runErr := apkrun.Command(ctx, "/sbin/apk", "update").CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk update failed: %s", tail(output))
	}
	if output, runErr := apkrun.Command(ctx, "/sbin/apk", append([]string{"add", "--"}, names...)...).CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk add failed: %s", tail(output))
	}
	return nil
}

func tail(output []byte) string {
	const limit = 4096
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return strings.TrimSpace(string(output))
}
