package vm

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/atomicfile"
)

// Scripts marked "run at startup" (Scripts window) run once per boot, after
// the web server listens: as root, in the background, each in a session of
// its own, so that restarting or updating the server leaves them running.
// A small shell wrapper keeps the first 64 KiB of their output and their exit
// status in /run (RAM, cleared at boot) without the server in between.
var (
	autostartList   = "/etc/kvm/autostart.list"
	autostartRunDir = "/run/nanokvm/autostart"
)

const autostartLogLimit = 64 << 10

// The wrapper: $1 status file, $2 log file, then the command. Output beyond
// the limit is read and dropped, so a talkative script never blocks or dies
// of SIGPIPE.
const autostartWrapper = `status=$1; log=$2; shift 2
( "$@"; echo "exited $?" > "$status" ) 2>&1 | { head -c %d > "$log"; cat > /dev/null; }`

// readAutostart returns the scripts marked to run at startup.
func readAutostart() ([]string, error) {
	data, err := os.ReadFile(autostartList)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		if name := strings.TrimSpace(line); validScriptName(name, true) {
			names = append(names, name)
		}
	}
	return names, nil
}

func writeAutostart(names []string) error {
	seen := map[string]bool{}
	unique := []string{}
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			unique = append(unique, name)
		}
	}
	sort.Strings(unique)
	if len(unique) == 0 {
		if err := os.Remove(autostartList); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return atomicfile.Write(autostartList, []byte(strings.Join(unique, "\n")+"\n"), 0o644)
}

// setAutostart marks or unmarks an existing script.
func setAutostart(root, name string, enabled bool) error {
	if enabled {
		if _, err := scriptPath(root, name); err != nil {
			return err
		}
	} else if !validScriptName(name, true) {
		return errInvalidScript
	}
	names, err := readAutostart()
	if err != nil {
		return err
	}
	kept := []string{}
	for _, current := range names {
		if current != name {
			kept = append(kept, current)
		}
	}
	if enabled {
		kept = append(kept, name)
	}
	return writeAutostart(kept)
}

// autostartBase names the log and status files of a script.
func autostartBase(name string) string {
	return filepath.Join(autostartRunDir, strings.ReplaceAll(name, "/", "__"))
}

// autostartStatus reports this boot's state of each marked script: running,
// exited N, or an error.
func autostartStatus(names []string) map[string]string {
	status := map[string]string{}
	for _, name := range names {
		if data, err := os.ReadFile(autostartBase(name) + ".status"); err == nil {
			status[name] = strings.TrimSpace(string(data))
		}
	}
	return status
}

// RunAutostart starts the marked scripts once per boot: a marker in /run,
// created before the first start, keeps server restarts from running them
// again.
func RunAutostart(root string) {
	if err := os.MkdirAll(autostartRunDir, 0o700); err != nil {
		log.Errorf("autostart: %s", err)
		return
	}
	marker, err := os.OpenFile(filepath.Join(autostartRunDir, ".started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if !errors.Is(err, os.ErrExist) {
			log.Errorf("autostart: %s", err)
		}
		return
	}
	marker.Close()
	names, err := readAutostart()
	if err != nil {
		log.Errorf("autostart: %s", err)
		return
	}
	for _, name := range names {
		if err := startAutostart(root, name); err != nil {
			log.Errorf("autostart %s: %s", name, err)
			_ = os.WriteFile(autostartBase(name)+".status", []byte("error: "+err.Error()+"\n"), 0o600)
			continue
		}
		log.Infof("autostart: started %s", name)
	}
}

func startAutostart(root, name string) error {
	path, err := scriptPath(root, name)
	if err != nil {
		return err
	}
	script, err := scriptCommand(path)
	if err != nil {
		return err
	}
	base := autostartBase(name)
	if err = os.WriteFile(base+".status", []byte("running\n"), 0o600); err != nil {
		return err
	}
	args := append([]string{"-c", fmt.Sprintf(autostartWrapper, autostartLogLimit), "nanokvm-autostart",
		base + ".status", base + ".log"}, script.Args...)
	cmd := exec.Command("/bin/sh", args...)
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	// Reap it while the server runs; after a restart init does.
	go func() { _ = cmd.Wait() }()
	return nil
}

// StartAutostartWhenListening waits up to a minute for the web server on
// host and port (all addresses when host is empty), then runs the marked
// scripts.
func StartAutostartWhenListening(host string, port int) {
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	address := net.JoinHostPort(host, fmt.Sprint(port))
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
			conn.Close()
			break
		}
	}
	RunAutostart(ScriptDirectory)
}
