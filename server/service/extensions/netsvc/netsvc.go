// Package netsvc holds the optional network services of Settings > Services:
// LLDP (lldpd), SNMP (net-snmp) and UPS (Network UPS Tools). Each is an
// Alpine package installed on request, an OpenRC service switched on and off,
// and a small configuration written from the settings.
package netsvc

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/extensions/apkpkg"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// unit is what the three services share: the packages, the program that tells
// whether they are installed, and the OpenRC service.
type unit struct {
	name     string   // in log lines
	packages []string // the program first, then what depends on it
	binary   string
	service  string
	// the daemon's pid file, where "started" for OpenRC is not enough
	pidfile string
	// before the first start: the configuration of a fresh installation
	prepare func() error

	// one install, removal, switch or settings change at a time
	mutex sync.Mutex
}

const runlevel = "/etc/runlevels/default"

func (u *unit) installed() bool {
	_, err := os.Stat(u.binary)
	return err == nil
}

// enabled: the service starts at boot. It can be enabled and not running
// (a UPS that is not attached keeps its driver from starting).
func (u *unit) enabled() bool {
	_, err := os.Lstat(filepath.Join(runlevel, u.service))
	return err == nil
}

func (u *unit) running() bool {
	return rc(5*time.Second, "rc-service", u.service, "status") == nil && u.alive()
}

// alive: the process of the pid file exists. A unit without one is taken at
// OpenRC's word.
func (u *unit) alive() bool {
	if u.pidfile == "" {
		return true
	}
	data, err := os.ReadFile(u.pidfile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	_, err = os.Stat("/proc/" + strconv.Itoa(pid))
	return err == nil
}

// stop leaves the service stopped for OpenRC too. A stop whose last step
// failed keeps the service marked as started with its daemon gone, and a
// start would then do nothing.
func (u *unit) stop() {
	_ = rc(30*time.Second, "rc-service", u.service, "stop")
	if u.pidfile != "" && !u.alive() {
		_ = rc(10*time.Second, "rc-service", u.service, "zap")
	}
}

// start does not fail: a service that does not start says so in its status.
func (u *unit) start() {
	if out, err := output(60*time.Second, "rc-service", u.service, "start"); err != nil {
		log.Warnf("%s did not start: %s", u.name, tail(out))
	}
}

// apply changes the configuration of the service. A running service is
// stopped first, with the configuration it was started with (NUT stops the
// driver named there), and started again with the new one.
func (u *unit) apply(write func() error) error {
	on := u.enabled()
	if on {
		u.stop()
	}
	err := write()
	if on {
		u.start()
	}
	return err
}

func rc(limit time.Duration, name string, args ...string) error {
	_, err := output(limit, name, args...)
	return err
}

func output(limit time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (u *unit) install() error {
	if err := apkpkg.InstallCommunity(u.packages...); err != nil {
		return err
	}
	if u.prepare != nil {
		return u.prepare()
	}
	return nil
}

func (u *unit) uninstall() error {
	u.stop()
	_ = rc(10*time.Second, "rc-update", "del", u.service, "default")
	// the dependants first: apk keeps a package another selected one needs
	for i := len(u.packages) - 1; i >= 0; i-- {
		if err := apkpkg.Run("remove", u.packages[i]); err != nil {
			return err
		}
	}
	return nil
}

// setEnabled adds the service to the boot and starts it, or the reverse. A
// service that is enabled but does not start is not an error here: its status
// says that it is not running. Enabling an enabled service starts it again.
func (u *unit) setEnabled(on bool) error {
	if on {
		if u.prepare != nil {
			if err := u.prepare(); err != nil {
				return err
			}
		}
		if err := rc(10*time.Second, "rc-update", "add", u.service, "default"); err != nil {
			return err
		}
		u.stop()
		u.start()
		return nil
	}
	u.stop()
	return rc(10*time.Second, "rc-update", "del", u.service, "default")
}

func tail(out []byte) string {
	text := strings.TrimSpace(string(out))
	if len(text) > 300 {
		text = text[len(text)-300:]
	}
	return text
}

// writeFile replaces a configuration file with a complete new one.
func writeFile(path string, data []byte, mode os.FileMode, group string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if group != "" {
		if err = rc(5*time.Second, "chgrp", group, f.Name()); err != nil {
			return err
		}
	}
	return os.Rename(f.Name(), path)
}

// secret is a random string of n letters and digits, for a community name or
// a password nobody has to remember.
func secret(n int) (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range raw {
		raw[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(raw), nil
}

// The marker in the first line of a configuration file written here. A file
// without it came with the package and is replaced at the first use.
const marker = "# Written by NanoKVM OS from Settings > Services; changes made here are replaced.\n"

func ours(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.HasPrefix(string(data), marker)
}

var errNotInstalled = errors.New("the service is not installed")

// The handlers common to the three services.

type enabledReq struct {
	Enabled *bool `json:"enabled" validate:"required"`
}

func (u *unit) handleInstall(c *gin.Context, status func() any) {
	var rsp proto.Response
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if err := u.install(); err != nil {
		log.Errorf("install %s: %v", u.name, err)
		rsp.ErrRsp(c, -1, "installation failed")
		return
	}
	rsp.OkRspWithData(c, status())
}

func (u *unit) handleUninstall(c *gin.Context, status func() any) {
	var rsp proto.Response
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if err := u.uninstall(); err != nil {
		log.Errorf("uninstall %s: %v", u.name, err)
		rsp.ErrRsp(c, -1, "removal failed")
		return
	}
	rsp.OkRspWithData(c, status())
}

func (u *unit) handleEnabled(c *gin.Context, status func() any) {
	var req enabledReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if !u.installed() {
		rsp.ErrRsp(c, -2, errNotInstalled.Error())
		return
	}
	if err := u.setEnabled(*req.Enabled); err != nil {
		log.Errorf("switch %s: %v", u.name, err)
		rsp.ErrRsp(c, -3, "could not switch the service")
		return
	}
	rsp.OkRspWithData(c, status())
}
