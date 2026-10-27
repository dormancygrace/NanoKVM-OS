package osupdate

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"

	"NanoKVM-Server/internal/apkrollback"
)

// The repository line is the persistent source of truth, including on images.
// Never rewrite mirrors, tagged overlays or unrelated Alpine repositories.
var channelRepository = regexp.MustCompile(`^https://dormancygrace\.github\.io/NanoKVM-OS/(beta/)?alpine/(v[0-9]+\.[0-9]+)/riscv64(?:/Packages\.adb)?/?$`)

type ChannelStatus struct {
	Channel           string `json:"channel"`
	Available         bool   `json:"available"`
	RollbackAvailable bool   `json:"rollbackAvailable"`
}

func channelLine(data string) (int, string, ChannelStatus) {
	found, branch := -1, ""
	status := ChannelStatus{Channel: "custom"}
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := channelRepository.FindStringSubmatch(line)
		if m == nil {
			if strings.Contains(strings.ToLower(line), "nanokvm") {
				return -1, "", ChannelStatus{Channel: "custom"}
			}
			continue
		}
		if found >= 0 {
			return -1, "", ChannelStatus{Channel: "custom"}
		}
		found, branch = i, m[2]
		status.Channel = "stable"
		if m[1] != "" {
			status.Channel = "beta"
		}
	}
	status.Available = found >= 0
	return found, branch, status
}

func GetChannel() ChannelStatus {
	data, err := os.ReadFile(repositoriesPath)
	if err != nil {
		return ChannelStatus{Channel: "custom"}
	}
	_, _, status := channelLine(string(data))
	if status.Available && status.Channel == "stable" {
		for _, component := range installedComponents() {
			if isCorePackage(component.Name) && prereleasePackage.MatchString(component.Installed) {
				status.RollbackAvailable = true
				break
			}
		}
	}
	return status
}

func SetChannel(channel string) error {
	if channel != "stable" && channel != "beta" {
		return errors.New("invalid update channel")
	}
	lock, err := updateLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	// Notice checks use their own cache and lock. Do not switch underneath one
	// or wait longer than the HTTP request: the caller can retry when it finishes.
	noticeLock, err := lockNotice(context.Background(), false)
	if err != nil {
		return err
	}
	if noticeLock == nil {
		return errors.New("update check is running; try again when it finishes")
	}
	defer noticeLock.Close()
	dbLock, err := lockCommunityDB()
	if err != nil {
		return err
	}
	defer dbLock.Close()
	data, err := os.ReadFile(repositoriesPath)
	if err != nil {
		return err
	}
	i, branch, status := channelLine(string(data))
	if !status.Available {
		return errors.New("update channel requires one official NanoKVM OS repository")
	}
	if status.Channel == channel {
		return nil
	}
	prefix := ""
	if channel == "beta" {
		prefix = "beta/"
	}
	lines := strings.Split(string(data), "\n")
	lines[i] = "https://dormancygrace.github.io/NanoKVM-OS/" + prefix + "alpine/" + branch + "/riscv64/Packages.adb"
	// Clear old offers before the atomic switch. Failure may lose a cached
	// check, but can never offer packages from the old channel as current.
	if err = saveNotice(UpdateNotice{State: "never", Updates: []SoftwareUpdate{}}); err != nil {
		return err
	}
	if err = saveAPKStatus(APKStatus{State: "idle"}); err != nil {
		return err
	}
	return writeAtomic(repositoriesPath, []byte(strings.Join(lines, "\n")), 0644)
}

// Keep in step with scripts/package-versions.py. Add-ons are independent and
// must never be included in a core firmware rollback.
var corePackages = apkrollback.CorePackages
var prereleasePackage = regexp.MustCompile(`_(alpha|beta|pre|rc)[0-9]*`)

func isCorePackage(name string) bool {
	for _, core := range corePackages {
		if name == core {
			return true
		}
	}
	return false
}

// A temporary solver constraint pins the whole core set for one transaction.
// Unlike upgrade --available, it does not broadly downgrade packages or clear
// the operator's world holds. A retry removes a leftover constraint first.
const rollbackConstraint = apkrollback.Constraint

func rollbackConstraintInstalled() bool {
	data, err := os.ReadFile(installedDBPath)
	return err == nil && strings.Contains("\n"+string(data), "\nP:"+rollbackConstraint+"\n")
}

// Require a complete stable set in the authenticated official index before
// invoking APK's solver. Missing packages must not leave beta components behind.
func stablePackageTargets(output string) ([]string, error) {
	found := map[string]string{}
	for _, line := range strings.Fields(output) {
		match := apkPackageVersion.FindStringSubmatch(line)
		if len(match) != 3 || !isCorePackage(match[1]) {
			continue
		}
		if prereleasePackage.MatchString(match[2]) {
			return nil, errors.New("stable repository contains prerelease packages")
		}
		if found[match[1]] != "" {
			return nil, errors.New("ambiguous stable package " + match[1])
		}
		found[match[1]] = match[2]
	}
	var targets []string
	for _, name := range corePackages {
		if found[name] == "" {
			if legacy := apkrollback.LegacyTargets(name); legacy != nil {
				targets = append(targets, legacy...)
				continue
			}
			return nil, errors.New("stable repository is missing " + name)
		}
		targets = append(targets, name+"="+found[name])
	}
	return targets, nil
}
