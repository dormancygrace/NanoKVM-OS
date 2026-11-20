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
			if changed := changedPackages(string(output), fix); len(changed) > 0 {
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

// changedPackages lists the packages of an apk simulation that are neither
// new nor a reinstall of a package to fix:
// "(2/5) Upgrading busybox (1.37.0-r18 -> 1.37.0-r19)". apk pads the step
// number to the width of the total: "( 2/12) Upgrading ...".
func changedPackages(simulation string, fix []string) []string {
	var changed []string
	for _, line := range strings.Split(simulation, "\n") {
		step := strings.Index(line, ") ")
		if !strings.HasPrefix(line, "(") || step < 0 {
			continue
		}
		fields := strings.Fields(line[step+2:])
		if len(fields) < 2 || fields[0] == "Installing" ||
			fields[0] == "Reinstalling" && slices.Contains(fix, fields[1]) {
			continue
		}
		changed = append(changed, fields[1])
	}
	return changed
}

// InstallAddon installs or updates an add-on of the NanoKVM repository. It
// refuses one whose minimum NanoKVM OS release is above the installed one,
// and any transaction that would change a package of the OS: apk meets an
// add-on's minimum release by upgrading the whole OS.
func InstallAddon(name string) error {
	if !packageName.MatchString(name) {
		return errors.New("invalid APK package name")
	}
	// The list is in /run, empty after a reboot until a check, and AI > Screen
	// text installs without opening the Add-ons page.
	if osupdate.GetAddonCatalog().CheckedAt == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		_ = osupdate.CheckUpdates(ctx, true)
		cancel()
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
	spec, err := osupdate.AddonInstallSpec(name)
	if err != nil {
		return err
	}
	// Select the catalog version without upgrading already satisfied OS dependencies.
	output, runErr := apkrun.Command(ctx, "/sbin/apk", "add", "--simulate", "--", spec).CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("apk add failed: %s", tail(output))
	}
	if changed := osupdate.CoreChanges(string(output), name); len(changed) > 0 {
		return fmt.Errorf("installing %s would change NanoKVM OS packages (%s); update NanoKVM OS first",
			name, strings.Join(changed, ", "))
	}
	if output, runErr = apkrun.Command(ctx, "/sbin/apk", "add", "--", spec).CombinedOutput(); runErr != nil {
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
