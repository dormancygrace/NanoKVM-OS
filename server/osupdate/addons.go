package osupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"NanoKVM-Server/internal/apkrollback"
)

// Add-ons are the packages of the NanoKVM repository that are not part of the
// OS: every nanokvm-* package outside nanokvm-release and its dependencies, those
// of the installed release and those of the release the index offers.
// The update check stores them from the index it has just fetched, so a new
// add-on appears on the Add-ons page with the next daily check, without an OS
// release. An add-on states the first OS release it works with as the
// dependency nanokvm-os>=VERSION, which nanokvm-release provides from 3.5.0.

// Addon is one add-on of the repository, with what this device has of it.
type Addon struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	License     string `json:"license,omitempty"`
	// MinOS is the first NanoKVM OS release the add-on works with; empty
	// when the add-on names none.
	MinOS     string `json:"minOs,omitempty"`
	Installed string `json:"installed,omitempty"`
	// Compatible: the installed OS is at least MinOS.
	Compatible bool `json:"compatible"`
}

// AddonCatalog is what the Add-ons page shows.
type AddonCatalog struct {
	// CheckedAt is the update check that stored the list, Unix seconds; 0
	// before the first one since boot.
	CheckedAt int64   `json:"checkedAt,omitempty"`
	OS        string  `json:"os,omitempty"`
	Addons    []Addon `json:"addons"`
	// The update check: its state and whether it runs by itself daily.
	State         string `json:"state"`
	NoticeEnabled bool   `json:"noticeEnabled"`
}

type storedCatalog struct {
	CheckedAt int64    `json:"checkedAt"`
	Core      []string `json:"core"`
	Addons    []Addon  `json:"addons"`
}

var addonPackage = regexp.MustCompile(`^nanokvm-[a-z0-9][a-z0-9+_.-]{0,119}$`)

// The OS packages of this server's release, for a device whose package database
// has no installed nanokvm-release to read them from.
var knownCore = apkrollback.CorePackages

// addonCorePackages: nanokvm-release and its dependencies, those of the
// installed release (the package database) and those of the indexed one
// (catalogCore). The installed release keeps an OS package that a cached index
// of an older release does not know yet out of the add-ons, and a package that
// a later release moved out of the OS is an add-on once that release is
// installed.
func addonCorePackages(catalogCore []string) map[string]bool {
	core := map[string]bool{"nanokvm-release": true}
	for _, name := range catalogCore {
		core[name] = true
	}
	installed := installedReleaseCore()
	if len(installed) == 0 {
		installed = knownCore
	}
	for _, name := range installed {
		core[name] = true
	}
	return core
}

// installedReleaseCore: the NanoKVM packages the installed nanokvm-release
// depends on, from its D: line in the package database.
func installedReleaseCore() []string {
	data, err := os.ReadFile(installedDBPath)
	if err != nil {
		return nil
	}
	var core []string
	release := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case line == "":
			release = false
		case strings.HasPrefix(line, "P:"):
			release = line == "P:nanokvm-release"
		case release && strings.HasPrefix(line, "D:"):
			for _, dep := range strings.Fields(line[2:]) {
				if strings.HasPrefix(dep, "!") {
					continue // a conflict, not a dependency
				}
				if name := dependencyName(dep); name == "nanokvm" || strings.HasPrefix(name, "nanokvm-") {
					core = append(core, name)
				}
			}
		}
	}
	return core
}

func addonCatalogPath() string { return apkStateDir + "/addons.json" }

type indexPackage struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	License     string   `json:"license"`
	Depends     []string `json:"depends"`
}

// refreshAddonCatalog lists the add-ons of the NanoKVM index in the notice
// cache, which the caller has just refreshed; it does not use the network.
func refreshAddonCatalog(ctx context.Context, repos []string, checkedAt int64) error {
	out, err := noticeApk(ctx, noticeArgs(repos, "--no-network", "query", "--format=json",
		"--fields=name,version,description,url,license,depends", "--from=repositories", "nanokvm-*")...)
	if err != nil {
		return fmt.Errorf("add-on query failed: %s", strings.TrimSpace(out))
	}
	stored, err := parseAddonIndex(out)
	if err != nil {
		return err
	}
	stored.CheckedAt = checkedAt
	b, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if old, e := os.ReadFile(addonCatalogPath()); e == nil && bytes.Equal(old, b) {
		return nil
	}
	return writeAtomic(addonCatalogPath(), b, 0600)
}

// parseAddonIndex: the newest version of each add-on of an `apk query
// --format json` listing of nanokvm-*.
func parseAddonIndex(out string) (storedCatalog, error) {
	// apk may print warnings before the JSON.
	start := strings.IndexByte(out, '[')
	if start < 0 {
		return storedCatalog{}, errors.New("add-on query printed no package list")
	}
	var packages []indexPackage
	if err := json.Unmarshal([]byte(out[start:]), &packages); err != nil {
		return storedCatalog{}, fmt.Errorf("add-on query: %w", err)
	}
	var release *indexPackage
	for i := range packages {
		if packages[i].Name == "nanokvm-release" && (release == nil || compareAPKVersions(packages[i].Version, release.Version) > 0) {
			release = &packages[i]
		}
	}
	if release == nil {
		return storedCatalog{}, errors.New("the NanoKVM index has no nanokvm-release")
	}
	// The indexed release's own OS packages; the installed release's are
	// added when the list is read (addonCorePackages).
	core := map[string]bool{"nanokvm-release": true}
	for _, dep := range release.Depends {
		if name := dependencyName(dep); name == "nanokvm" || strings.HasPrefix(name, "nanokvm-") {
			core[name] = true
		}
	}
	newest := map[string]Addon{}
	for _, p := range packages {
		if core[p.Name] || !addonPackage.MatchString(p.Name) {
			continue
		}
		if old, ok := newest[p.Name]; ok && compareAPKVersions(p.Version, old.Version) <= 0 {
			continue
		}
		a := Addon{Name: p.Name, Version: p.Version, Description: p.Description, URL: p.URL, License: p.License}
		for _, dep := range p.Depends {
			if v, ok := strings.CutPrefix(dep, "nanokvm-os>="); ok {
				a.MinOS = v
			}
		}
		newest[p.Name] = a
	}
	stored := storedCatalog{Core: make([]string, 0, len(core)), Addons: make([]Addon, 0, len(newest))}
	for name := range core {
		stored.Core = append(stored.Core, name)
	}
	sort.Strings(stored.Core)
	for _, a := range newest {
		stored.Addons = append(stored.Addons, a)
	}
	sort.Slice(stored.Addons, func(i, j int) bool { return stored.Addons[i].Name < stored.Addons[j].Name })
	return stored, nil
}

// dependencyName: the package of a dependency such as "nanokvm-web>=3.4.0-r0".
func dependencyName(dep string) string {
	dep = strings.TrimPrefix(dep, "!")
	if i := strings.IndexAny(dep, "<>=~"); i >= 0 {
		dep = dep[:i]
	}
	return dep
}

// installedOS is the release of the installed nanokvm-release, without -rN.
func installedOS(installed map[string]string) string {
	v := installed["nanokvm-release"]
	if i := strings.LastIndex(v, "-r"); i > 0 {
		v = v[:i]
	}
	return v
}

func readCatalog() (storedCatalog, bool) {
	var stored storedCatalog
	b, err := os.ReadFile(addonCatalogPath())
	if err != nil || json.Unmarshal(b, &stored) != nil {
		return storedCatalog{}, false
	}
	return stored, true
}

// GetAddonCatalog joins the stored list with the installed packages: a file
// read and the installed database, no apk run.
func GetAddonCatalog() AddonCatalog {
	notice := GetNotice()
	c := AddonCatalog{Addons: []Addon{}, State: notice.State, NoticeEnabled: notice.Enabled}
	installed, _ := installedVersions()
	c.OS = installedOS(installed)
	stored, _ := readCatalog()
	c.CheckedAt = stored.CheckedAt
	core := addonCorePackages(stored.Core)
	listed := map[string]bool{}
	for _, a := range stored.Addons {
		if core[a.Name] {
			continue
		}
		listed[a.Name] = true
		a.Installed = installed[a.Name]
		a.Compatible = a.MinOS == "" || (c.OS != "" && compareAPKVersions(c.OS, a.MinOS) >= 0)
		c.Addons = append(c.Addons, a)
	}
	// An installed add-on the repository no longer lists can still be removed.
	for name, version := range installed {
		if addonPackage.MatchString(name) && !core[name] && !listed[name] {
			c.Addons = append(c.Addons, Addon{Name: name, Installed: version, Compatible: true})
		}
	}
	sort.Slice(c.Addons, func(i, j int) bool { return c.Addons[i].Name < c.Addons[j].Name })
	return c
}

// AddonInstallSpec validates an add-on and selects at least its catalog version.
// A lower bound updates the add-on without recursively upgrading dependencies
// as apk add -u does, or pinning it against future updates. The name is in the
// stored list and the installed OS is at least its minimum release. apk would
// meet an unmet minimum by upgrading nanokvm-release, that is the whole OS.
func AddonInstallSpec(name string) (string, error) {
	c := GetAddonCatalog()
	if c.CheckedAt == 0 {
		return "", errors.New("the add-on list has not been loaded since the device started; check the repository first")
	}
	for _, a := range c.Addons {
		if a.Name != name {
			continue
		}
		if a.Version == "" {
			return "", errors.New("the repository no longer offers this add-on")
		}
		if !a.Compatible {
			return "", fmt.Errorf("%s needs NanoKVM OS %s or later; update NanoKVM OS first", name, a.MinOS)
		}
		return name + ">=" + a.Version, nil
	}
	return "", errors.New("not an add-on of the NanoKVM repository")
}

// IsInstalledAddon: name is installed and is not part of the OS. Before the
// first list since boot nothing counts as an add-on.
func IsInstalledAddon(name string) bool {
	c := GetAddonCatalog()
	if c.CheckedAt == 0 {
		return false
	}
	for _, a := range c.Addons {
		if a.Name == name {
			return a.Installed != ""
		}
	}
	return false
}

// CoreChanges lists the packages of the OS that an `apk add --simulate`
// transcript would upgrade, downgrade, replace or remove: an add-on install
// must leave the OS as it is.
func CoreChanges(simulation, addon string) []string {
	stored, _ := readCatalog()
	core := addonCorePackages(stored.Core)
	var changed []string
	for _, line := range strings.Split(simulation, "\n") {
		fields := strings.Fields(line)
		// "(2/5) Upgrading nanokvm-base (3.5.0-r0 -> 3.6.0-r0)"
		if len(fields) < 3 || !strings.HasPrefix(fields[0], "(") {
			continue
		}
		switch fields[1] {
		case "Upgrading", "Downgrading", "Replacing", "Purging", "Removing":
		default:
			continue
		}
		name := fields[2]
		if name != addon && (core[name] || strings.HasPrefix(name, "nanokvm-")) {
			changed = append(changed, name)
		}
	}
	return changed
}

var addonCheckTimeout = 2*noticeTimeout + 10*time.Second

// EnsureAddonCatalog starts a check when there is no list since boot and the
// daily check is on, so the Add-ons page fills by itself after a reboot.
func EnsureAddonCatalog() {
	if _, ok := readCatalog(); ok || !NoticeEnabled() || noticeChecking.Load() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), addonCheckTimeout)
		defer cancel()
		_ = CheckUpdates(ctx, true)
	}()
}

// compareAPKVersions orders APK versions as apk does for the forms used here:
// dot-separated numbers, an optional letter, _alpha/_beta/_pre/_rc (before the
// release) or _cvs/_svn/_git/_hg/_p (after) suffixes with optional numbers,
// and -rN. Anything else compares as text.
func compareAPKVersions(a, b string) int {
	pa, okA := parseAPKVersion(a)
	pb, okB := parseAPKVersion(b)
	if !okA || !okB {
		return strings.Compare(a, b)
	}
	for i := 0; i < len(pa.numbers) || i < len(pb.numbers); i++ {
		if i >= len(pa.numbers) {
			return -1
		}
		if i >= len(pb.numbers) {
			return 1
		}
		if c := compareInt(pa.numbers[i], pb.numbers[i]); c != 0 {
			return c
		}
	}
	if pa.letter != pb.letter {
		return compareInt(int(pa.letter), int(pb.letter))
	}
	for i := 0; i < len(pa.suffixes) || i < len(pb.suffixes); i++ {
		sa, sb := apkSuffix{}, apkSuffix{}
		if i < len(pa.suffixes) {
			sa = pa.suffixes[i]
		}
		if i < len(pb.suffixes) {
			sb = pb.suffixes[i]
		}
		if c := compareInt(sa.rank, sb.rank); c != 0 {
			return c
		}
		if c := compareInt(sa.number, sb.number); c != 0 {
			return c
		}
	}
	return compareInt(pa.release, pb.release)
}

type apkSuffix struct{ rank, number int }

type apkVersion struct {
	numbers  []int
	letter   byte
	suffixes []apkSuffix
	release  int
}

var suffixRank = map[string]int{"alpha": -4, "beta": -3, "pre": -2, "rc": -1, "cvs": 1, "svn": 2, "git": 3, "hg": 4, "p": 5}

var apkVersionForm = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)([a-z]?)((?:_[a-z]+[0-9]*)*)(?:-r([0-9]+))?$`)

func parseAPKVersion(v string) (apkVersion, bool) {
	m := apkVersionForm.FindStringSubmatch(v)
	if m == nil {
		return apkVersion{}, false
	}
	var p apkVersion
	for _, part := range strings.Split(m[1], ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return apkVersion{}, false
		}
		p.numbers = append(p.numbers, n)
	}
	if m[2] != "" {
		p.letter = m[2][0]
	}
	for _, s := range strings.Split(strings.TrimPrefix(m[3], "_"), "_") {
		if s == "" {
			continue
		}
		name := strings.TrimRight(s, "0123456789")
		rank, ok := suffixRank[name]
		if !ok {
			return apkVersion{}, false
		}
		number := 0
		if digits := s[len(name):]; digits != "" {
			number, _ = strconv.Atoi(digits)
		}
		p.suffixes = append(p.suffixes, apkSuffix{rank, number})
	}
	if m[4] != "" {
		p.release, _ = strconv.Atoi(m[4])
	}
	return p, true
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
