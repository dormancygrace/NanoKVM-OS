package apkpkg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"NanoKVM-Server/osupdate"

	log "github.com/sirupsen/logrus"
)

// basePackages: the network services of Settings > Services and the OpenVPN
// client. The image installs them with nanokvm-release (platform/build.sh),
// which does not depend on them: a release that required community packages
// could not be installed by a device whose list lacks the community line, and
// nanokvm-base adds that line only once its update is installed. A device
// updated from a release without them gets them here instead.
var basePackages = []string{"lldpd", "lldpd-openrc", "net-snmp", "net-snmp-openrc", "nut", "nut-openrc", "openvpn"}

// baseMarker records that every base package was installed once, complete and
// selected in the APK world, with every package they depend on complete. A
// package removed on the Software page after
// that stays removed.
const baseMarker = "/etc/kvm/base-packages"

// The first attempt waits for the start to settle; a failed one is repeated
// every few minutes (a busy update lock, an offline device). A transaction
// that would change installed packages stays so until the OS is updated, which
// restarts the server: it is asked again less and less often.
const (
	baseFirstTry = 2 * time.Minute
	baseRetry    = 5 * time.Minute
	baseMaxRetry = 6 * time.Hour
)

// EnsureBasePackages installs the base packages a device lacks, in the
// background until it succeeds. Their services stay off: nothing is added to a
// runlevel.
func EnsureBasePackages(ctx context.Context) {
	if _, err := os.Stat(baseMarker); err == nil {
		return
	}
	delay, refused := baseFirstTry, baseRetry
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		err := installBase()
		if err == nil {
			return
		}
		if err.Error() != last {
			log.Warnf("base packages: %v; trying again later", err)
			last = err.Error()
		}
		delay = baseRetry
		if errors.Is(err, errChangesInstalled) {
			delay, refused = refused, min(2*refused, baseMaxRetry)
		}
	}
}

// installBase writes the marker only when every base package is installed,
// complete and selected, and every package they depend on is complete, as read
// back after the transaction: a failed extraction or install script can leave
// a package recorded but broken, and that is repaired on the next attempt.
func installBase() error {
	fix, add, err := baseWork()
	if err != nil {
		return err
	}
	if len(fix)+len(add) > 0 {
		if err = installCommunity(true, fix, add); err != nil {
			return err
		}
		if fix, add, err = baseWork(); err != nil {
			return err
		}
		if len(fix)+len(add) > 0 {
			return fmt.Errorf("not installed completely: %s", strings.Join(append(fix, add...), ", "))
		}
		log.Infof("base packages installed")
	}
	if err = os.MkdirAll(filepath.Dir(baseMarker), 0o755); err != nil {
		return err
	}
	return os.WriteFile(baseMarker, nil, 0o644)
}

func baseWork() (fix, add []string, err error) {
	missing, broken, err := osupdate.PackageState(basePackages...)
	if err != nil {
		return nil, nil, err
	}
	world, err := os.ReadFile(worldPath)
	if err != nil {
		return nil, nil, err
	}
	fix, add = basePlan(missing, broken, world)
	return fix, add, nil
}

// basePlan: the packages to reinstall with `apk fix`, base packages and the
// broken packages they depend on, and the base packages to select with
// `apk add`. A dependency is not added to the world. A package installed only as a dependency of another one is
// not in the world, and apk would remove it with that one; `apk add` of an
// installed package only adds it to the world. A package the world already
// names, with a version constraint, a repository tag or a file pin, keeps
// that entry: when it is not installed, `apk fix` installs it as the world
// asks. A package the world excludes (!name) stays out.
func basePlan(missing, broken []string, world []byte) (fix, add []string) {
	worldEntries := strings.Fields(string(world))
	for _, name := range basePackages {
		if slices.Contains(worldEntries, "!"+name) {
			continue
		}
		chosen := selected(world, name)
		if !chosen {
			add = append(add, name)
		}
		if slices.Contains(broken, name) || chosen && slices.Contains(missing, name) {
			fix = append(fix, name)
		}
	}
	for _, name := range broken {
		if !slices.Contains(basePackages, name) {
			fix = append(fix, name)
		}
	}
	return fix, add
}
