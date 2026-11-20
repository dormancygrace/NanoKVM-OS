package apkpkg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// baseMarker records that every base package was installed once. A package
// removed on the Software page after that stays removed.
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

func installBase() error {
	missing, err := osupdate.MissingPackages(basePackages...)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		if err = installCommunity(true, missing); err != nil {
			return err
		}
		log.Infof("base packages installed: %v", missing)
	}
	if err = os.MkdirAll(filepath.Dir(baseMarker), 0o755); err != nil {
		return err
	}
	return os.WriteFile(baseMarker, nil, 0o644)
}
