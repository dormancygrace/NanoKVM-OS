// Package netsvc holds the network services of Settings > Services: LLDP
// (lldpd), SNMP (net-snmp) and UPS (Network UPS Tools). Each is an Alpine
// package installed with the system (apkpkg.EnsureBasePackages on an updated
// device), an OpenRC service that is off until it is switched on, and a small
// configuration written from the settings.
package netsvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// unit is what the three services share: the program that tells whether
// they are installed, and the OpenRC service.
type unit struct {
	name    string // in log lines
	binary  string
	service string
	// the daemon's pid file: "started" for OpenRC is not enough
	pidfile string
	// the files the settings are written to
	configs []string
	// what the daemon needs besides its files, before a start
	access func()
	// a configuration written here is there, all of it; nil for a service
	// that runs with its package's own
	complete func() bool
	// writes the configuration of a fresh installation over what is there
	fresh func() error

	// one switch or settings change at a time
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

// state is what a status reports. The dashboard asks for all three services
// every 30 s and shows only those that are on: its light status skips the
// configuration audit of a service that is off and has no daemon, which is not
// running. The service page shows the foreign warning in any state.
func (u *unit) state(light bool) (enabled, running, foreign bool) {
	enabled = u.enabled()
	if light && !enabled && !u.alive() {
		return enabled, false, false
	}
	return enabled, u.running(), u.foreignNow()
}

func (u *unit) running() bool {
	return rc(5*time.Second, "rc-service", u.service, "status") == nil && u.alive()
}

// alive: the daemon is there, as the process of its pid file or as any
// process of its program (a stop that failed can leave one without the file).
func (u *unit) alive() bool {
	if data, err := os.ReadFile(u.pidfile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if _, err = os.Stat("/proc/" + strconv.Itoa(pid)); err == nil {
				return true
			}
		}
	}
	return rc(5*time.Second, "pidof", filepath.Base(u.binary)) == nil
}

// stop returns nil when the daemon is stopped. A stop that fails with the
// daemon still there is an error, and the caller changes nothing. One that
// fails with the daemon gone (NUT: the last step, stopping the driver) keeps
// the service marked as started, and a start would then do nothing: the mark
// is removed.
func (u *unit) stop() error {
	out, err := output(30*time.Second, "rc-service", u.service, "stop")
	if err == nil {
		return nil
	}
	if u.alive() {
		return fmt.Errorf("%s did not stop: %s", u.name, tail(out))
	}
	_ = rc(10*time.Second, "rc-service", u.service, "zap")
	return nil
}

// start does not fail: a service that does not start says so in its status.
func (u *unit) start() {
	if out, err := output(60*time.Second, "rc-service", u.service, "start"); err != nil {
		log.Warnf("%s did not start: %s", u.name, tail(out))
	}
}

// apply changes the configuration of the service. A running service is
// stopped first, with the configuration it was started with (NUT stops the
// driver named there), and started again with the new one, or with the one
// it had when the write fails.
func (u *unit) apply(write func() error) error {
	on := u.enabled() || u.alive()
	if on {
		if err := u.stop(); err != nil {
			return err
		}
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

// prepare, before a start, replaces the package's example configuration
// with the one of a fresh installation, until a complete one is there. One
// that somebody wrote stays.
func (u *unit) prepare() error {
	if u.access != nil {
		u.access()
	}
	if u.complete == nil || u.complete() {
		return nil
	}
	if foreign, err := u.foreign(); foreign || err != nil {
		return err
	}
	return u.fresh()
}

// setEnabled adds the service to the boot and starts it, or the reverse. A
// service that is enabled but does not start is not an error here: its status
// says that it is not running. Enabling an enabled service starts it again.
func (u *unit) setEnabled(on bool) error {
	if on {
		if err := u.prepare(); err != nil {
			return err
		}
		// the boot entry after the stop: a daemon that does not stop leaves
		// the service as it was, at the next boot too
		if err := u.stop(); err != nil {
			return err
		}
		if err := rc(10*time.Second, "rc-update", "add", u.service, "default"); err != nil {
			return err
		}
		u.start()
		return nil
	}
	if err := u.stop(); err != nil {
		return err
	}
	return rc(10*time.Second, "rc-update", "del", u.service, "default")
}

func tail(out []byte) string {
	text := strings.TrimSpace(string(out))
	if len(text) > 300 {
		text = text[len(text)-300:]
	}
	return text
}

// stage writes the new content of a configuration file next to it and
// returns the name of that file, to be renamed over the old one.
func stage(path string, data []byte, mode os.FileMode, group string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return "", err
	}
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
	if err == nil && group != "" {
		err = rc(5*time.Second, "chgrp", group, f.Name())
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// writeFile replaces a configuration file with a complete new one.
func writeFile(path string, data []byte, mode os.FileMode, group string) error {
	return writeFiles(map[string][]byte{path: data}, mode, group)
}

// writeFiles replaces several configuration files, all of them or none. A
// file that already has its content is left alone. Every new file is
// complete on the disk before the first old one goes; when one cannot be put
// in place, those already replaced get their previous content back.
func writeFiles(files map[string][]byte, mode os.FileMode, group string) (err error) {
	paths := make([]string, 0, len(files))
	previous := map[string][]byte{}
	for path, data := range files {
		old, readErr := os.ReadFile(path)
		if readErr == nil {
			if bytes.Equal(old, data) {
				continue
			}
			previous[path] = old
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	staged := make([]string, 0, len(paths))
	defer func() {
		for _, name := range staged {
			os.Remove(name)
		}
	}()
	for _, path := range paths {
		name, stageErr := stage(path, files[path], mode, group)
		if stageErr != nil {
			return stageErr
		}
		staged = append(staged, name)
	}
	for i, path := range paths {
		if err = os.Rename(staged[i], path); err == nil {
			continue
		}
		for _, done := range paths[:i] {
			data, was := previous[done]
			if !was {
				os.Remove(done)
			} else if name, stageErr := stage(done, data, mode, group); stageErr == nil {
				if os.Rename(name, done) != nil {
					os.Remove(name)
				}
			}
		}
		return err
	}
	return nil
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
// without it is the package's own, or one somebody wrote (see foreign).
const marker = "# Written by NanoKVM OS from Settings > Services; changes made here are replaced.\n"

func ours(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.HasPrefix(string(data), marker)
}

// foreign: one of the configuration files is neither written here nor the
// package's own, so somebody configured the service by hand. Such a
// configuration is used as it is and never written over, until it is
// replaced on request (handleReset). apk tells the two apart: its audit of a
// directory lists the files that differ from their package's, as in
// "U etc/snmp/snmpd.conf", and the ones no package has.
func (u *unit) foreign() (bool, error) {
	unknown := map[string]bool{}
	dirs := map[string]bool{}
	for _, path := range u.configs {
		data, err := os.ReadFile(path)
		if err != nil || strings.HasPrefix(string(data), marker) {
			continue
		}
		unknown[strings.TrimPrefix(path, "/")] = true
		dirs[filepath.Dir(path)] = true
	}
	for dir := range dirs {
		out, err := output(30*time.Second, "/sbin/apk", "audit", dir)
		if err != nil {
			return false, fmt.Errorf("apk audit %s: %s", dir, tail(out))
		}
		for _, line := range strings.Split(string(out), "\n") {
			if fields := strings.Fields(line); len(fields) == 2 && unknown[fields[1]] {
				return true, nil
			}
		}
	}
	return false, nil
}

// foreignNow is foreign for a status: a configuration that could not be
// examined is not offered for editing.
func (u *unit) foreignNow() bool {
	foreign, err := u.foreign()
	if err != nil {
		log.Warnf("%s configuration: %v", u.name, err)
	}
	return foreign || err != nil
}

var (
	errNotInstalled = errors.New("the service is not installed")
	errForeign      = errors.New("the service has a configuration that was not written here")
)

// writable is the check of every settings change: nil when the configuration
// is ours to write.
func (u *unit) writable() error {
	foreign, err := u.foreign()
	if err != nil {
		return err
	}
	if foreign {
		return errForeign
	}
	return nil
}

// The handlers common to the three services.

type enabledReq struct {
	Enabled *bool `json:"enabled" validate:"required"`
}

// handleReset replaces the configuration, whoever wrote it, with the one of a
// fresh installation.
func (u *unit) handleReset(c *gin.Context, status func(light bool) any) {
	var rsp proto.Response
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if !u.installed() {
		rsp.ErrRsp(c, -2, errNotInstalled.Error())
		return
	}
	if err := u.apply(u.fresh); err != nil {
		log.Errorf("reset %s: %v", u.name, err)
		rsp.ErrRsp(c, -3, "could not replace the configuration")
		return
	}
	rsp.OkRspWithData(c, status(false))
}

// handleSettings writes the settings a handler has parsed and checked.
func (u *unit) handleSettings(c *gin.Context, status func(light bool) any, write func() error) {
	var rsp proto.Response
	u.mutex.Lock()
	defer u.mutex.Unlock()
	if !u.installed() {
		rsp.ErrRsp(c, -2, errNotInstalled.Error())
		return
	}
	if err := u.writable(); err != nil {
		log.Errorf("%s settings: %v", u.name, err)
		rsp.ErrRsp(c, -5, errForeign.Error())
		return
	}
	if err := u.apply(write); err != nil {
		log.Errorf("%s settings: %v", u.name, err)
		rsp.ErrRsp(c, -3, "could not save the settings")
		return
	}
	rsp.OkRspWithData(c, status(false))
}

func (u *unit) handleEnabled(c *gin.Context, status func(light bool) any) {
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
	rsp.OkRspWithData(c, status(false))
}
