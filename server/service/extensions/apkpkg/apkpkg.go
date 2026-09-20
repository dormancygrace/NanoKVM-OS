package apkpkg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

// InstallTagged installs packages from one explicitly tagged Alpine
// repository. The tag keeps edge packages from becoming candidates for
// unrelated upgrades on the stable base system.
func InstallTagged(tag, repository string, names ...string) error {
	if !packageName.MatchString(tag) || !strings.HasPrefix(repository, "https://") || len(names) == 0 {
		return errors.New("invalid tagged APK repository")
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
	if err = ensureRepository(tag, repository); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if output, runErr := exec.CommandContext(ctx, "/sbin/apk", "update").CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk update failed: %s", tail(output))
	}
	args := []string{"add", "--"}
	for _, name := range names {
		args = append(args, name+"@"+tag)
	}
	if output, runErr := exec.CommandContext(ctx, "/sbin/apk", args...).CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk add failed: %s", tail(output))
	}
	return nil
}

func ensureRepository(tag, repository string) error {
	const path = "/etc/apk/repositories"
	line := "@" + tag + " " + repository
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, existing := range strings.Split(string(data), "\n") {
		fields := strings.Fields(existing)
		if len(fields) > 0 && fields[0] == "@"+tag {
			if strings.TrimSpace(existing) == line {
				return nil
			}
			return fmt.Errorf("APK repository tag @%s already points elsewhere", tag)
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, line...)
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".repositories-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err = temporary.Chmod(0644); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporary.Name(), path)
	}
	return err
}

func tail(output []byte) string {
	const limit = 4096
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return strings.TrimSpace(string(output))
}
