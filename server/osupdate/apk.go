package osupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/internal/apkrollback"
	"NanoKVM-Server/internal/apkrun"
)

var apkStateDir = "/run/nanokvm-apk-ui"

var apkExecutable = "/sbin/apk"

var (
	worldPath     = "/etc/apk/world"
	apkDBLockPath = "/lib/apk/db/lock"
)

// errAPKDBBusy means apk holds its database lock.
var errAPKDBBusy = errors.New("apk database is in use")

var installedDBCache struct {
	sync.Mutex
	path     string
	mtime    time.Time
	size     int64
	versions map[string]string
}

// installedVersions: the version of every installed package. The update
// notice asks for it on every menu poll, so the file is parsed once per
// change.
func installedVersions() (map[string]string, error) {
	info, err := os.Stat(installedDBPath)
	if err != nil {
		return nil, err
	}
	c := &installedDBCache
	c.Lock()
	defer c.Unlock()
	if c.versions == nil || c.path != installedDBPath || !info.ModTime().Equal(c.mtime) || info.Size() != c.size {
		data, err := os.ReadFile(installedDBPath)
		if err != nil {
			return nil, err
		}
		c.versions = parseInstalledDB(string(data))
		c.path, c.mtime, c.size = installedDBPath, info.ModTime(), info.Size()
	}
	return maps.Clone(c.versions), nil
}

// PackageState lists the named packages that are not installed, and the
// installed packages apk recorded as broken among the named ones and
// everything they depend on, directly or through other packages. A package
// whose files could not be extracted or whose install script failed stays in
// the installed database, with "f" (files) or "s" (script) on its f: line,
// until `apk fix` installs it again: these are the flags on which apk fails
// the transaction and which `apk fix` repairs. "x" (extended attributes) and
// "S" (checksum kind) do not count. A package that depends on a name no
// installed package provides is broken too: apk installs and selects a
// requested package when the archive of one of its dependencies cannot be
// downloaded. apk records a requested package as
// complete when only the install script of one of its dependencies failed, so
// the dependencies are checked too.
func PackageState(names ...string) (missing, broken []string, err error) {
	data, err := os.ReadFile(installedDBPath)
	if err != nil {
		return nil, nil, err
	}
	db := parsePackageGraph(string(data))
	seen := map[string]bool{}
	queue := []string{}
	for _, name := range names {
		if _, installed := db.healthy[name]; !installed {
			missing = append(missing, name)
		} else if !seen[name] {
			seen[name] = true
			queue = append(queue, name)
		}
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		// A package whose dependency nothing installed provides was installed
		// without it (a download that failed): `apk fix` of the package
		// installs the dependency again.
		unprovided := slices.ContainsFunc(db.depends[name], func(dep string) bool { return len(db.providers[dep]) == 0 })
		if !db.healthy[name] || unprovided {
			broken = append(broken, name)
		}
		for _, dep := range db.depends[name] {
			for _, provider := range db.providers[dep] {
				if !seen[provider] {
					seen[provider] = true
					queue = append(queue, provider)
				}
			}
		}
	}
	return missing, broken, nil
}

// dependencyName is the package name of a dependency, without its version
// constraint: "so:libc.musl-riscv64.so.1", "busybox>=1.37".
func dependencyName(dep string) string {
	if i := strings.IndexAny(dep, "<>=~"); i >= 0 {
		dep = dep[:i]
	}
	return dep
}

// packageGraph is the installed database reduced to what PackageState needs:
// whether each package was installed completely, the names each one depends
// on, and the packages that provide each name.
type packageGraph struct {
	healthy   map[string]bool
	depends   map[string][]string
	providers map[string][]string
}

// parsePackageGraph reads P: (name), p: (provided names such as
// "so:libnetsnmp.so.45=45.0.0" or "/bin/sh"), D: (dependencies, "!name" being
// a conflict) and f: (flags) of every package record.
func parsePackageGraph(data string) packageGraph {
	db := packageGraph{healthy: map[string]bool{}, depends: map[string][]string{}, providers: map[string][]string{}}
	name := ""
	for _, line := range strings.Split(data, "\n") {
		switch {
		case line == "":
			name = ""
		case strings.HasPrefix(line, "P:"):
			name = line[2:]
			db.healthy[name] = true
			db.providers[name] = append(db.providers[name], name)
		case name == "":
		case strings.HasPrefix(line, "p:"):
			for _, provided := range strings.Fields(line[2:]) {
				provided, _, _ = strings.Cut(provided, "=")
				db.providers[provided] = append(db.providers[provided], name)
			}
		case strings.HasPrefix(line, "D:"):
			for _, dep := range strings.Fields(line[2:]) {
				if !strings.HasPrefix(dep, "!") {
					db.depends[name] = append(db.depends[name], dependencyName(dep))
				}
			}
		case strings.HasPrefix(line, "f:"):
			if strings.ContainsAny(line[2:], "fs") {
				db.healthy[name] = false
			}
		}
	}
	return db
}

func parseInstalledDB(data string) map[string]string {
	versions := map[string]string{}
	name := ""
	for _, line := range strings.Split(data, "\n") {
		switch {
		case line == "":
			name = ""
		case strings.HasPrefix(line, "P:"):
			name = line[2:]
		case strings.HasPrefix(line, "V:") && name != "":
			versions[name] = line[2:]
		}
	}
	return versions
}

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
// back to the repositories (nanokvmFilePins).
func nanokvmFilePins() []string {
	var names []string
	for _, entry := range worldEntries() {
		if name, _, pinned := strings.Cut(entry, "><"); pinned && isNanoKVMSelection(name) {
			names = append(names, name)
		}
	}
	return names
}

// Repository tags are preserved when returning a file selection to a repository.
func isNanoKVMSelection(selection string) bool {
	name, _, _ := strings.Cut(selection, "@")
	return name == "nanokvm" || strings.HasPrefix(name, "nanokvm-")
}

// upgradeSelections identifies entries that cannot use apk version's highest
// repository version. File pins outside NanoKVM remain held. Exact, upper and
// fuzzy bounds use the version selected by apk's solver instead: <2 or ~1.0
// can still allow upgrades, including a version below the repository's latest.
func upgradeSelections() (skip, bounded []string) {
	for _, entry := range worldEntries() {
		if name, _, pinned := strings.Cut(entry, "><"); pinned {
			if !isNanoKVMSelection(name) {
				skip = append(skip, strings.SplitN(name, "@", 2)[0])
			}
			continue
		}
		i := strings.IndexAny(entry, "=<>~")
		if i < 0 || entry[i] == '>' {
			continue
		}
		name := strings.SplitN(entry[:i], "@", 2)[0]
		skip = append(skip, name)
		bounded = append(bounded, name)
	}
	return skip, bounded
}

// apkStepNumber is the start of every step line of an apk transaction: the
// step number padded to the width of the total, "( 2/12)", or "(?)" when apk
// cannot format it.
const apkStepNumber = `^\((?:\s*[0-9]+/[0-9]+|\?)\)\s+`

// apkStep is one step of an apk transaction as apk-tools prints it:
// "(2/5) Upgrading nanokvm-base (3.5.0-r0 -> 3.6.0-r0)" or
// "( 3/12) Installing nut@community (2.8.2-r0)". The action is one of the
// messages apk-tools has, some of them several words; a package from a tagged
// repository carries the tag after its name.
var (
	apkStepLine = regexp.MustCompile(apkStepNumber)
	apkStep     = regexp.MustCompile(apkStepNumber +
		`(Installing|Purging|Reinstalling|\[APK unavailable, skipped\] Reinstalling|` +
		`Updating pinning|Downgrading|Replacing|Upgrading)\s+([^\s@]+)(?:@\S+)?\s+\((.*)\)$`)
)

// parseAPKStep is the action, package name without its repository tag and
// parenthesised detail (the version, or "old -> new") of a transaction step
// line; ok is false for any other line, including a step in a form apk-tools
// does not print.
func parseAPKStep(line string) (action, name, detail string, ok bool) {
	m := apkStep.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

// OtherInstallChanges reports APK transaction steps other than installing a
// requested package. An unrecognized numbered step also stops the install.
func OtherInstallChanges(simulation string, names []string) []string {
	var changed []string
	for _, line := range strings.Split(simulation, "\n") {
		action, name, _, ok := parseAPKStep(line)
		if !ok {
			if line = strings.TrimSpace(line); apkStepLine.MatchString(line) {
				changed = append(changed, line)
			}
			continue
		}
		if action != "Installing" || !slices.Contains(names, name) {
			changed = append(changed, action+" "+name)
		}
	}
	return changed
}

// InstalledChanges reports the APK transaction steps that change an installed
// package: every step but installing a new package or reinstalling one of
// reinstall (a package `apk fix` repairs). An unrecognized numbered step also
// counts.
func InstalledChanges(simulation string, reinstall []string) []string {
	var changed []string
	for _, line := range strings.Split(simulation, "\n") {
		action, name, _, ok := parseAPKStep(line)
		if !ok {
			if line = strings.TrimSpace(line); apkStepLine.MatchString(line) {
				changed = append(changed, line)
			}
			continue
		}
		if action != "Installing" && !(action == "Reinstalling" && slices.Contains(reinstall, name)) {
			changed = append(changed, action+" "+name)
		}
	}
	return changed
}

// Only upgrades of the bounded packages are added; installations, removals
// and holds are not.
func boundedUpgrades(plan string, bounded []string) string {
	var lines []string
	for _, line := range strings.Split(plan, "\n") {
		action, name, detail, ok := parseAPKStep(line)
		if !ok || action != "Upgrading" || !slices.Contains(bounded, name) {
			continue
		}
		from, to, found := strings.Cut(detail, " -> ")
		if !found || strings.ContainsAny(from+to, " ()") {
			continue
		}
		lines = append(lines, name+"-"+from+" < "+to)
	}
	return strings.Join(lines, "\n")
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
			if fields[0] != m[1]+"-"+version {
				fields[0] = m[1] + "-" + version
				line = strings.Join(fields, " ")
			}
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
		skip, bounded := upgradeSelections()
		upgrades := installableUpgrades(string(b), skip)
		if len(bounded) > 0 {
			plan, err := apkrun.Command(ctx, apkExecutable, "upgrade", "--simulate").CombinedOutput()
			if err != nil {
				return fmt.Errorf("APK upgrade plan failed: %s", strings.TrimSpace(string(plan)))
			}
			upgrades = strings.TrimSpace(upgrades + "\n" + boundedUpgrades(string(plan), bounded))
		}
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
			// If resolving the repository selections fails, log the error
			// and still attempt the remaining upgrades.
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
		var output, stderr strings.Builder
		search := apkrun.Command(ctx, apkExecutable, query...)
		search.Stdout, search.Stderr = &output, &stderr
		// search fails when a name is not in the index, as a core package is
		// not in a release from before it (LegacyTargets); it still lists the
		// others, and stablePackageTargets checks that nothing else is missing.
		if err := search.Run(); err != nil && (apkrun.NotStarted(err) || strings.TrimSpace(output.String()) == "") {
			return fmt.Errorf("cannot read stable packages: %s", strings.TrimSpace(stderr.String()))
		}
		targets, err := stablePackageTargets(output.String())
		if err != nil {
			return err
		}
		db, _ := os.ReadFile(installedDBPath)
		if names := tpuDependents(targets, string(db)); len(names) > 0 {
			return fmt.Errorf("stable has no TPU runtime; remove %s first", strings.Join(names, ", "))
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
