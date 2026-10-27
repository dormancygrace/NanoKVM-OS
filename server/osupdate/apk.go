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
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"NanoKVM-Server/internal/apkrollback"
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
	if s.State == "ready" {
		// Packages updated since the check, from a shell too, are left out.
		if installed, err := installedVersions(); err == nil {
			s.Upgrades = pendingUpgrades(s.Upgrades, installed)
			if s.Upgrades == "" {
				s.State = "up-to-date"
			}
		}
	}
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

// A package installed from a local file (`apk add ./name.apk`) stays selected
// as that file in the world file, name><Q1checksum: apk upgrade keeps it while
// apk version lists the newer repository version, so the page offered an
// update that installing never made. Installing updates takes NanoKVM packages
// back to the repositories (nanokvmFilePins); heldPackages are not offered.
func nanokvmFilePins() []string {
	var names []string
	for _, entry := range worldEntries() {
		if name, _, pinned := strings.Cut(entry, "><"); pinned && strings.HasPrefix(name, "nanokvm") {
			names = append(names, name)
		}
	}
	return names
}

// heldPackages are those apk upgrade keeps where they are: an exact or upper
// version bound (name=1.0-r0, name<2, name~1.2), a deliberate choice, or a
// local file selection of a package other than NanoKVM's. A lower bound
// (name>=1.0) still upgrades.
func heldPackages() []string {
	var names []string
	for _, entry := range worldEntries() {
		if name, _, pinned := strings.Cut(entry, "><"); pinned {
			if !strings.HasPrefix(name, "nanokvm") {
				names = append(names, strings.SplitN(name, "@", 2)[0])
			}
			continue
		}
		i := strings.IndexAny(entry, "=<>~")
		if i < 0 || entry[i] == '>' {
			continue
		}
		names = append(names, strings.SplitN(entry[:i], "@", 2)[0])
	}
	return names
}

func worldEntries() []string {
	data, err := os.ReadFile(worldPath)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

var apkVersionedName = regexp.MustCompile(`^(.+)-[0-9][^-]*-r[0-9]+$`)

// installableUpgrades is the `apk version --limit <` listing without its
// header and without the packages in skip.
func installableUpgrades(listing string, skip []string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "Installed:" {
			continue
		}
		if m := apkVersionedName.FindStringSubmatch(fields[0]); m != nil && slices.Contains(skip, m[1]) {
			continue
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	return strings.Join(lines, "\n")
}

// pendingUpgrades keeps the lines of a checked upgrade listing whose package
// is still installed below the available version.
func pendingUpgrades(listing string, installed map[string]string) string {
	var lines []string
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		m := apkVersionedName.FindStringSubmatch(fields[0])
		if m == nil {
			lines = append(lines, line)
			continue
		}
		if version, ok := installed[m[1]]; ok && compareAPKVersions(fields[len(fields)-1], version) > 0 {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
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
		upgrades := installableUpgrades(string(b), heldPackages())
		state := "ready"
		if upgrades == "" {
			state = "up-to-date"
		}
		// The notice has an index of its own; a manual check refreshes it too.
		_ = CheckUpdates(ctx, true)
		return saveAPKStatus(APKStatus{State: state, Upgrades: upgrades})
	}
	if action == "install" {
		if names := nanokvmFilePins(); len(names) > 0 {
			fmt.Printf("Taking %s back to the NanoKVM repositories: installed from local files\n", strings.Join(names, ", "))
			// A test build the repositories never carried cannot be selected
			// there; it stays as it is and the other updates still install.
			if e := run(append([]string{"add"}, names...)...); e != nil {
				fmt.Printf("Kept the local file selections: %v\n", e)
			}
		}
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
		query := apkrollback.Query(repo)
		output, err := apkrun.Command(ctx, apkExecutable, query...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("cannot read stable packages: %s", strings.TrimSpace(string(output)))
		}
		targets, err := stablePackageTargets(string(output))
		if err != nil {
			return err
		}
		args = apkrollback.Install(targets)
		// APK checks the complete dependency graph before installing anything.
		if err = run(append(append([]string{}, args...), "--simulate")...); err != nil {
			return fmt.Errorf("stable rollback dependency check failed: %w", err)
		}
	}
	if action == "rollback" {
		if err := installRollbackRecovery(); err != nil {
			return fmt.Errorf("cannot prepare persistent rollback recovery: %w", err)
		}
		// Also remove the hold if a package script fails after APK installed it.
		defer func() {
			if rollbackConstraintInstalled() {
				if err := run("del", rollbackConstraint); err != nil && result == nil {
					result = fmt.Errorf("stable installed but temporary constraint cleanup failed; reboot to recover: %w", err)
				}
			}
		}()
	}
	if e := run(args...); e != nil {
		return fmt.Errorf("APK package transaction failed: %w", e)
	}
	if action == "rollback" {
		if err := run("del", rollbackConstraint); err != nil {
			return fmt.Errorf("stable constraint cleanup failed; reboot to recover: %w", err)
		}
		if err := removeRollbackRecovery(); err != nil {
			return err
		}
	}
	_ = CheckUpdates(ctx, false)
	return saveAPKStatus(APKStatus{State: "installed"})
}
