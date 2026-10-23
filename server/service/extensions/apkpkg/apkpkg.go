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
	return install(false, names...)
}

// InstallCommunity installs packages of Alpine's community repository, which
// is off by default: it switches the repository on first.
func InstallCommunity(names ...string) error {
	return install(true, names...)
}

func install(community bool, names ...string) error {
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
	if community {
		if err = osupdate.EnableCommunityLocked(); err != nil {
			return fmt.Errorf("cannot enable the Alpine community repository: %w", err)
		}
	}
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

// InstallAddon installs or updates an add-on of the NanoKVM repository. It
// refuses one whose minimum NanoKVM OS release is above the installed one,
// and any transaction that would change a package of the OS: apk meets an
// add-on's minimum release by upgrading the whole OS.
func InstallAddon(name string) error {
	if !packageName.MatchString(name) {
		return errors.New("invalid APK package name")
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
	if err = osupdate.CheckAddon(name); err != nil {
		return err
	}
	// -u: an installed add-on goes to the repository's version.
	output, runErr := apkrun.Command(ctx, "/sbin/apk", "add", "-u", "--simulate", "--", name).CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("apk add failed: %s", tail(output))
	}
	if changed := osupdate.CoreChanges(string(output), name); len(changed) > 0 {
		return fmt.Errorf("installing %s would change NanoKVM OS packages (%s); update NanoKVM OS first",
			name, strings.Join(changed, ", "))
	}
	if output, runErr = apkrun.Command(ctx, "/sbin/apk", "add", "-u", "--", name).CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk add failed: %s", tail(output))
	}
	return nil
}

// RemoveAddon removes an installed add-on, never a package of the OS.
func RemoveAddon(name string) error {
	if !osupdate.IsInstalledAddon(name) {
		return errors.New("not an installed add-on")
	}
	return Run("remove", name)
}

func tail(output []byte) string {
	const limit = 4096
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return strings.TrimSpace(string(output))
}
