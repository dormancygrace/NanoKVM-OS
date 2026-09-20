package osupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	softwareLogLimit  = 64 * 1024
	softwareIndexAge  = 24 * time.Hour
	softwareIndexPath = "/var/cache/apk"
)

var apkPackageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.-]{0,127}$`)
var apkPackageVersion = regexp.MustCompile(`^(.+)-([0-9][A-Za-z0-9._+~:-]*)$`)

// SoftwarePackage is deliberately small: package catalogues can be large and
// are searched page by page rather than sent wholesale to the browser.
type SoftwarePackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type SoftwareUpdate struct {
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Available string `json:"available"`
}

// SoftwareIndexStatus describes the local APK repository indexes. APK cannot
// determine whether a remote repository has changed without contacting it, so
// an index older than one day is reported as potentially stale.
type SoftwareIndexStatus struct {
	State        string `json:"state"`
	UpdatedAt    int64  `json:"updatedAt,omitempty"`
	AgeSeconds   int64  `json:"ageSeconds,omitempty"`
	Repositories int    `json:"repositories"`
	Cached       int    `json:"cached"`
}

type SoftwareStatus struct {
	State   string `json:"state"`
	Action  string `json:"action,omitempty"`
	Package string `json:"package,omitempty"`
	Message string `json:"message,omitempty"`
	Log     string `json:"log,omitempty"`
}

func softwareStatePath() string { return apkStateDir + "/software.json" }
func softwareLogPath() string   { return apkStateDir + "/software.log" }

func GetSoftwareStatus() SoftwareStatus {
	s := SoftwareStatus{State: "idle"}
	b, _ := os.ReadFile(softwareStatePath())
	_ = json.Unmarshal(b, &s)
	if b, err := readTail(softwareLogPath(), softwareLogLimit); err == nil {
		s.Log = string(b)
	}
	return s
}

func saveSoftwareStatus(s SoftwareStatus) error {
	if err := os.MkdirAll(apkStateDir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeAtomic(softwareStatePath(), b, 0600)
}

func readTail(name string, limit int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > limit {
		_, _ = f.Seek(-limit, 2)
	}
	return ioReadAllLimit(f, limit)
}

// ioReadAllLimit keeps operation responses bounded on constrained devices.
func ioReadAllLimit(f *os.File, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(f, limit))
}

func validPackageName(name string) error {
	if !apkPackageName.MatchString(name) {
		return errors.New("invalid package name")
	}
	return nil
}

var protectedPackages = map[string]bool{
	"apk-tools":               true,
	"busybox":                 true,
	"musl":                    true,
	"openrc":                  true,
	"nanokvm-app":             true,
	"nanokvm-base":            true,
	"nanokvm-release":         true,
	"nanokvm-kernel-sg2002":   true,
	"nanokvm-kmod-sg2002":     true,
	"nanokvm-firmware-sg2002": true,
}

func checkRemovable(name string) error {
	if err := validPackageName(name); err != nil {
		return err
	}
	if protectedPackages[name] {
		return errors.New("this protected system package cannot be removed from the web interface")
	}
	return nil
}

func parseMatchingPackages(output, query string, max int) []SoftwarePackage {
	seen := make(map[string]bool)
	packages := make([]SoftwarePackage, 0, max)
	query = strings.ToLower(query)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, " - ", 2)[0])
		match := apkPackageVersion.FindStringSubmatch(line)
		if len(match) != 3 || !apkPackageName.MatchString(match[1]) || seen[match[1]] {
			continue
		}
		if query != "" && !strings.Contains(match[1], query) {
			continue
		}
		seen[match[1]] = true
		packages = append(packages, SoftwarePackage{Name: match[1], Version: match[2]})
		if len(packages) == max {
			break
		}
	}
	sort.Slice(packages, func(i, j int) bool {
		if query != "" {
			rank := func(name string) int {
				switch {
				case name == query:
					return 0
				case strings.HasPrefix(name, query):
					return 1
				default:
					return 2
				}
			}
			if left, right := rank(packages[i].Name), rank(packages[j].Name); left != right {
				return left < right
			}
		}
		return packages[i].Name < packages[j].Name
	})
	return packages
}

func parsePackages(output string, max int) []SoftwarePackage {
	return parseMatchingPackages(output, "", max)
}

func parseSoftwareUpdates(output string) []SoftwareUpdate {
	updates := make([]SoftwareUpdate, 0)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		installed, available := fields[0], fields[1]
		if len(fields) >= 3 && fields[1] == "<" {
			available = fields[2]
		}
		current := apkPackageVersion.FindStringSubmatch(installed)
		candidate := apkPackageVersion.FindStringSubmatch(available)
		if len(current) != 3 || len(candidate) != 3 || !apkPackageName.MatchString(current[1]) {
			continue
		}
		updates = append(updates, SoftwareUpdate{Name: current[1], Installed: current[2], Available: candidate[2]})
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].Name < updates[j].Name })
	return updates
}

func GetSoftwareIndexStatus() SoftwareIndexStatus {
	status := SoftwareIndexStatus{State: "missing"}
	if repositories, err := os.ReadFile("/etc/apk/repositories"); err == nil {
		for _, line := range strings.Split(string(repositories), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				status.Repositories++
			}
		}
	}
	entries, err := os.ReadDir(softwareIndexPath)
	if err != nil {
		return status
	}
	var oldest time.Time
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "APKINDEX.") || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		status.Cached++
		if oldest.IsZero() || info.ModTime().Before(oldest) {
			oldest = info.ModTime()
		}
	}
	if oldest.IsZero() {
		return status
	}
	status.UpdatedAt = oldest.Unix()
	status.AgeSeconds = int64(time.Since(oldest).Seconds())
	if status.AgeSeconds < 0 {
		status.AgeSeconds = 0
	}
	if status.Repositories > status.Cached || time.Duration(status.AgeSeconds)*time.Second > softwareIndexAge {
		status.State = "stale"
		return status
	}
	status.State = "fresh"
	return status
}

func runAPKOutput(ctx context.Context, args ...string) (string, error) {
	b, err := exec.CommandContext(ctx, apkExecutable, args...).CombinedOutput()
	if len(b) > softwareLogLimit {
		b = b[len(b)-softwareLogLimit:]
	}
	return string(b), err
}

func ListInstalledSoftware() ([]SoftwarePackage, error) {
	if _, err := os.Stat("/etc/alpine-release"); err != nil {
		return nil, errors.New("native APK software management requires Alpine")
	}
	// On the NanoKVM, even the installed-package query can take around twenty
	// seconds after the APK database has grown. Keep it below the browser limit.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	output, err := runAPKOutput(ctx, "info", "--verbose")
	if err != nil {
		return nil, fmt.Errorf("cannot list installed packages: %s", strings.TrimSpace(output))
	}
	return parsePackages(output, 512), nil
}

func SearchSoftware(query string) ([]SoftwarePackage, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if err := validPackageName(query); err != nil {
		return nil, errors.New("enter a package name using letters, digits, +, _, . or -")
	}
	if _, err := os.Stat("/etc/alpine-release"); err != nil {
		return nil, errors.New("native APK software management requires Alpine")
	}
	// Package catalogue scans run on the NanoKVM's low-power CPU and can take
	// longer than the lightweight installed-package query. Keep this below the
	// browser request timeout while allowing APK to finish a normal search.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	output, err := runAPKOutput(ctx, "search", "--all", "--verbose", "*"+query+"*")
	if err != nil {
		return nil, fmt.Errorf("package search failed: %s", strings.TrimSpace(output))
	}
	// apk also matches package descriptions. Filter its output by package name
	// so a search for "mc" starts with mc, mcabber, and similar packages.
	return parseMatchingPackages(output, query, 50), nil
}

func ListSoftwareUpdates() ([]SoftwareUpdate, error) {
	if _, err := os.Stat("/etc/alpine-release"); err != nil {
		return nil, errors.New("native APK software management requires Alpine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	output, err := runAPKOutput(ctx, "version", "-l", "<")
	if err != nil {
		return nil, fmt.Errorf("cannot check available package updates: %s", strings.TrimSpace(output))
	}
	return parseSoftwareUpdates(output), nil
}

func PreviewSoftwareRemoval(name string) (string, error) {
	if err := checkRemovable(name); err != nil {
		return "", err
	}
	lock, err := Lock()
	if err != nil {
		return "", err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := runAPKOutput(ctx, "del", "--simulate", "--", name)
	if err != nil {
		return "", fmt.Errorf("cannot simulate package removal: %s", strings.TrimSpace(output))
	}
	return output, nil
}

func StartSoftware(action, name string) error {
	if action != "refresh" && action != "install" && action != "remove" && action != "upgrade" {
		return errors.New("invalid software operation")
	}
	if action == "install" {
		if err := validPackageName(name); err != nil {
			return err
		}
	} else if action == "remove" {
		if err := checkRemovable(name); err != nil {
			return err
		}
	} else if name != "" {
		return errors.New("this software operation does not accept a package name")
	}
	if _, err := os.Stat("/etc/alpine-release"); err != nil {
		return errors.New("native APK software management requires Alpine")
	}
	lock, err := Lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = saveSoftwareStatus(SoftwareStatus{State: "running", Action: action, Package: name}); err != nil {
		return err
	}
	log, err := os.OpenFile(softwareLogPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(Helper, "software-inherited", action, name)
	cmd.ExtraFiles = []*os.File{lock}
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		_ = saveSoftwareStatus(SoftwareStatus{State: "failed", Action: action, Package: name, Message: err.Error()})
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// RunSoftware runs only in the inherited-lock helper, so it remains alive if
// upgrading an APK replaces the HTTP server that initiated the operation.
func RunSoftware(action, name string) (result error) {
	if action == "install" {
		if err := validPackageName(name); err != nil {
			return err
		}
	} else if action == "remove" {
		if err := checkRemovable(name); err != nil {
			return err
		}
	} else if (action != "refresh" && action != "upgrade") || name != "" {
		return errors.New("invalid software operation")
	}
	status := SoftwareStatus{State: "failed", Action: action, Package: name}
	defer func() {
		if result != nil {
			status.Message = result.Error()
			_ = saveSoftwareStatus(status)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, apkExecutable, args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	if action == "refresh" {
		result = run("update")
	} else if action == "install" {
		if result = run("update"); result == nil {
			result = run("add", "--", name)
		}
	} else if action == "remove" {
		result = run("del", "--", name)
	} else if action == "upgrade" {
		if result = run("update"); result == nil {
			result = run("upgrade")
		}
	} else {
		result = errors.New("invalid software operation")
	}
	if result != nil {
		return fmt.Errorf("APK %s failed: %w", action, result)
	}
	return saveSoftwareStatus(SoftwareStatus{State: "succeeded", Action: action, Package: name, Message: "APK operation completed"})
}
