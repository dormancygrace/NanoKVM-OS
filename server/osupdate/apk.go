package osupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"NanoKVM-Server/internal/apkrun"
)

var apkStateDir = "/run/nanokvm-apk-ui"

var apkExecutable = "/sbin/apk"

type APKStatus struct {
	State    string `json:"state"`
	Message  string `json:"message,omitempty"`
	Upgrades string `json:"upgrades,omitempty"`
	Log      string `json:"log,omitempty"`
	Reboot   bool   `json:"reboot"`
	Kernel   string `json:"kernel"`
}

func GetAPKStatus() APKStatus {
	s := APKStatus{State: "idle"}
	b, _ := os.ReadFile(apkStateDir + "/state.json")
	_ = json.Unmarshal(b, &s)
	_, e := os.Stat("/run/reboot-required")
	s.Reboot = e == nil
	b, _ = os.ReadFile("/proc/sys/kernel/osrelease")
	s.Kernel = strings.TrimSpace(string(b))
	if f, e := os.Open(apkStateDir + "/operation.log"); e == nil {
		defer f.Close()
		if st, e := f.Stat(); e == nil && st.Size() > 65536 {
			_, _ = f.Seek(-65536, io.SeekEnd)
		}
		b, _ = io.ReadAll(io.LimitReader(f, 65536))
		s.Log = string(b)
	}
	return s
}
func saveAPKStatus(s APKStatus) error {
	if e := os.MkdirAll(apkStateDir, 0700); e != nil {
		return e
	}
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	tmp := filepath.Join(apkStateDir, "state.tmp")
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, apkStateDir+"/state.json")
}
func StartAPK(action string) error {
	if action != "check" && action != "install" && action != "rollback" && action != "reboot" {
		return errors.New("invalid package operation")
	}
	if _, e := os.Stat("/etc/alpine-release"); e != nil {
		return errors.New("native APK updates require Alpine")
	}
	lock, e := Lock()
	if e != nil {
		return e
	}
	defer lock.Close()
	state := map[string]string{"check": "checking", "install": "installing", "rollback": "installing", "reboot": "rebooting"}[action]
	if e = saveAPKStatus(APKStatus{State: state}); e != nil {
		return e
	}
	log, e := os.OpenFile(apkStateDir+"/operation.log", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd := exec.Command(Helper, "apk-inherited", action)
	cmd.ExtraFiles = []*os.File{lock}
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		_ = saveAPKStatus(APKStatus{State: "failed", Message: e.Error()})
		return e
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Runs in a detached helper so upgrading/restarting the web server cannot stop APK.
func RunAPK(action string) (result error) {
	defer func() {
		if result != nil {
			_ = saveAPKStatus(APKStatus{State: "failed", Message: result.Error()})
		}
	}()
	if action == "reboot" {
		time.Sleep(time.Second)
		return exec.Command("/sbin/reboot").Run()
	}
	if action != "check" && action != "install" && action != "rollback" {
		return errors.New("invalid package operation")
	}
	if action == "rollback" {
		if status := GetChannel(); !status.Available || status.Channel != "stable" || !status.RollbackAvailable {
			return errors.New("select Stable with a prerelease installed before rolling back")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	run := func(args ...string) error {
		cmd := apkrun.Command(ctx, apkExecutable, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if e := run("update"); e != nil {
		// The page has one check for both: an unreachable Alpine mirror must
		// not keep the NanoKVM OS repository from being checked.
		if action == "check" {
			_ = CheckUpdates(ctx, true)
		}
		return fmt.Errorf("APK index refresh failed: %w", e)
	}
	if rollbackConstraintInstalled() {
		if err := run("del", rollbackConstraint); err != nil {
			return fmt.Errorf("cannot clear interrupted rollback constraint: %w", err)
		}
	}
	if action == "check" {
		b, e := apkrun.Command(ctx, apkExecutable, "version", "--limit", "<").CombinedOutput()
		if e != nil {
			return fmt.Errorf("APK upgrade check failed: %s", strings.TrimSpace(string(b)))
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(lines) > 0 && strings.HasPrefix(lines[0], "Installed:") {
			lines = lines[1:]
		}
		upgrades := strings.TrimSpace(strings.Join(lines, "\n"))
		state := "ready"
		if upgrades == "" {
			state = "up-to-date"
		}
		// The notice has an index of its own; a manual check refreshes it too.
		_ = CheckUpdates(ctx, true)
		return saveAPKStatus(APKStatus{State: state, Upgrades: upgrades})
	}
	args := []string{"upgrade"}
	if action == "rollback" {
		data, err := os.ReadFile(repositoriesPath)
		if err != nil {
			return err
		}
		i, _, status := channelLine(string(data))
		if !status.Available || status.Channel != "stable" {
			return errors.New("stable repository changed")
		}
		repo := strings.TrimSpace(strings.Split(string(data), "\n")[i])
		query := append([]string{"--repositories-file=/dev/null", "--repository=" + repo, "--no-network", "search", "--exact"}, corePackages...)
		output, err := apkrun.Command(ctx, apkExecutable, query...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("cannot read stable packages: %s", strings.TrimSpace(string(output)))
		}
		targets, err := stablePackageTargets(string(output))
		if err != nil {
			return err
		}
		args = append([]string{"add", "--virtual", rollbackConstraint}, targets...)
		// APK checks the complete dependency graph before installing anything.
		if err = run(append(append([]string{}, args...), "--simulate")...); err != nil {
			return fmt.Errorf("stable rollback dependency check failed: %w", err)
		}
	}
	if action == "rollback" {
		// Also remove the hold if a package script fails after APK installed it.
		defer func() {
			if rollbackConstraintInstalled() {
				if err := run("del", rollbackConstraint); err != nil && result == nil {
					result = fmt.Errorf("stable installed but temporary constraint cleanup failed; retry the update check: %w", err)
				}
			}
		}()
	}
	if e := run(args...); e != nil {
		return fmt.Errorf("APK package transaction failed: %w", e)
	}
	if action == "rollback" {
		if err := run("del", rollbackConstraint); err != nil {
			return fmt.Errorf("stable constraint cleanup failed: %w", err)
		}
	}
	_ = CheckUpdates(ctx, false)
	return saveAPKStatus(APKStatus{State: "installed"})
}
