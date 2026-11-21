package apkpkg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
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

// InstallCommunity installs packages of Alpine's community repository. It
// lists the repository first, for a list that lost the line. The packages are
// installed together, after refreshing the indexes, under the same lock as
// other package operations.
func InstallCommunity(names ...string) error {
	return installCommunity(false, nil, names)
}

// installCommunity is InstallCommunity that first reinstalls the fix packages
// (`apk fix`). With alone, it refuses a transaction that would upgrade,
// downgrade, replace or remove an installed package, so that the install
// never carries part of an OS update; reinstalling a fix package is allowed.
func installCommunity(alone bool, fix, names []string) error {
	if len(fix)+len(names) == 0 {
		return errors.New("no APK package to install")
	}
	for _, name := range append(slices.Clone(fix), names...) {
		if !packageName.MatchString(name) {
			return errors.New("invalid APK package name")
		}
	}
	lock, err := osupdate.Lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = osupdate.EnableCommunityLocked(); err != nil {
		return fmt.Errorf("cannot enable the Alpine community repository: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if output, runErr := apkrun.Command(ctx, "/sbin/apk", "update").CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk update failed: %s", tail(output))
	}
	for _, step := range []struct {
		applet string
		names  []string
	}{{"fix", fix}, {"add", names}} {
		if len(step.names) == 0 {
			continue
		}
		if alone {
			output, runErr := apkrun.Command(ctx, "/sbin/apk", append([]string{step.applet, "--simulate", "--"}, step.names...)...).CombinedOutput()
			if runErr != nil {
				return fmt.Errorf("apk %s failed: %s", step.applet, tail(output))
			}
			if changed := osupdate.InstalledChanges(string(output), fix); len(changed) > 0 {
				return fmt.Errorf("installing %s %w (%s)",
					strings.Join(step.names, ", "), errChangesInstalled, strings.Join(changed, ", "))
			}
		}
		output, runErr := apkrun.Command(ctx, "/sbin/apk", append([]string{step.applet, "--"}, step.names...)...).CombinedOutput()
		if runErr != nil {
			return fmt.Errorf("apk %s failed: %s", step.applet, tail(output))
		}
	}
	return nil
}

var errChangesInstalled = errors.New("would change installed packages")

// InstallOnly installs packages of the NanoKVM repositories and changes no
// installed package: it refreshes the indexes, simulates the transaction and
// refuses it when apk would do anything but install names, so callers name
// every package the installation brings. A package that needs a later
// NanoKVM OS (nanokvm-os>=VERSION) would otherwise upgrade the whole OS as a
// side effect of an install.
func InstallOnly(names ...string) error {
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
	output, runErr := apkrun.Command(ctx, "/sbin/apk", append([]string{"add", "--simulate", "--"}, names...)...).CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("apk add failed: %s", tail(output))
	}
	if changed := osupdate.OtherInstallChanges(string(output), names); len(changed) > 0 {
		return fmt.Errorf("installing %s would change other packages (%s); update NanoKVM OS first",
			strings.Join(names, ", "), strings.Join(changed, ", "))
	}
	if output, runErr = apkrun.Command(ctx, "/sbin/apk", append([]string{"add", "--"}, names...)...).CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk add failed: %s", tail(output))
	}
	return nil
}

// Remove removes packages in one apk transaction, under the same lock as other
// package operations.
func Remove(names ...string) error {
	if len(names) == 0 {
		return errors.New("no APK package to remove")
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if output, runErr := apkrun.Command(ctx, "/sbin/apk", append([]string{"del", "--"}, names...)...).CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk del failed: %s", tail(output))
	}
	return nil
}

// Keep selects an installed package in the APK world, so that apk keeps it
// once no other package depends on it. It downloads nothing (--no-network);
// callers check that their tools are installed before selecting the package.
func Keep(name string) error {
	if !packageName.MatchString(name) {
		return errors.New("invalid APK package name")
	}
	world, _ := os.ReadFile(worldPath)
	if selected(world, name) {
		return nil
	}
	lock, err := osupdate.Lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if output, runErr := apkrun.Command(ctx, "/sbin/apk", "add", "--no-network", "--", name).CombinedOutput(); runErr != nil {
		return fmt.Errorf("apk add failed: %s", tail(output))
	}
	return nil
}

var worldPath = "/etc/apk/world"

// Selected reports whether the APK world names the package: whether it was
// chosen, not only installed as another package's dependency.
func Selected(name string) (bool, error) {
	world, err := os.ReadFile(worldPath)
	if err != nil {
		return false, err
	}
	return selected(world, name), nil
}

// selected reports whether the world names the package, with or without a
// version constraint, a repository tag or a file pin.
func selected(world []byte, name string) bool {
	for _, entry := range strings.Fields(string(world)) {
		if i := strings.IndexAny(entry, "=<>~@"); i >= 0 {
			entry = entry[:i]
		}
		if entry == name {
			return true
		}
	}
	return false
}

func tail(output []byte) string {
	const limit = 4096
	if len(output) > limit {
		output = output[len(output)-limit:]
	}
	return strings.TrimSpace(string(output))
}
