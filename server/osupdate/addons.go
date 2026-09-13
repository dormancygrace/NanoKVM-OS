package osupdate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

const addonWorld = "/opt/nkos/etc/apk/world"
const addonManager = "/usr/sbin/nkos-addons"

var addonNameRE = regexp.MustCompile(`^nkos-addon-[a-z0-9][a-z0-9+_.-]{0,63}$`)
var addonIntentRE = regexp.MustCompile(`^(nkos-addon-[a-z0-9][a-z0-9+_.-]{0,63})(?:[<>=~]{1,2}[0-9A-Za-z][0-9A-Za-z.+_~-]{0,95})?$`)
var imageProviderRE = regexp.MustCompile(`^(?:nkos-base-abi|nkos-server-api|nkos-feature-[a-z0-9][a-z0-9-]{0,63})(?:=[0-9A-Za-z][0-9A-Za-z.+_~-]{0,95})?$`)
var addonPackagePathRE = regexp.MustCompile(`^packages/[a-z0-9][a-z0-9+_.-]*\.apk$`)

type addonRestorePackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

type addonRestoreCheckpoint struct {
	Format         int                   `json:"format"`
	ContractSHA256 string                `json:"contract_sha256"`
	World          []string              `json:"world"`
	Packages       []addonRestorePackage `json:"packages"`
}

func readAddonWorld(root string) ([]string, error) {
	name := filepath.Join(root, addonWorld)
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 64<<10 {
		return nil, errors.New("unsafe addon world file")
	}
	fd, err := syscall.Open(name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "addon-world")
	defer f.Close()
	seen := map[string]bool{}
	var world []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		name := strings.TrimSpace(strings.SplitN(s.Text(), "#", 2)[0])
		if name == "" {
			continue
		}
		if imageProviderRE.MatchString(name) {
			continue
		}
		match := addonIntentRE.FindStringSubmatch(name)
		if len(match) == 0 || seen[match[1]] {
			return nil, errors.New("addon world contains an unsupported package expression; remove it before updating")
		}
		seen[match[1]] = true
		world = append(world, name)
		if len(world) > 256 {
			return nil, errors.New("too many installed addons")
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	sort.Strings(world)
	return world, nil
}

type addonPrepareRunner func(manager, contract, restore string, lock *os.File) error

func runAddonPrepare(manager, contract, restore string, lock *os.File) error {
	cmd := exec.Command(manager, "prepare-upgrade", contract, restore)
	cmd.ExtraFiles = []*os.File{lock}
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		return fmt.Errorf("addon upgrade preflight failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func prepareAddonUpgrade(root, stage, contractHash string, lock *os.File) error {
	return prepareAddonUpgradeWith(root, stage, contractHash, lock, runAddonPrepare)
}

func prepareAddonUpgradeWith(root, stage, contractHash string, lock *os.File, runner addonPrepareRunner) error {
	world, err := readAddonWorld(root)
	if err != nil {
		return err
	}
	restore := filepath.Join(stage, "addon-restore")
	if err = os.Mkdir(restore, 0700); err != nil {
		return err
	}
	if len(world) == 0 {
		if err = writeJSON(filepath.Join(restore, "checkpoint.json"), addonRestoreCheckpoint{Format: 1, ContractSHA256: contractHash, World: []string{}, Packages: []addonRestorePackage{}}); err != nil {
			return err
		}
	} else {
		info, statErr := os.Lstat(filepath.Join(root, addonManager))
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return errors.New("installed addons require the addon migration manager; remove addons before this OS update")
		}
		if lock == nil {
			return errors.New("addon migration requires the canonical update lock")
		}
		if err = runner(filepath.Join(root, addonManager), filepath.Join(stage, "addons-contract.json"), restore, lock); err != nil {
			return err
		}
	}
	return validateAddonCheckpoint(restore, contractHash, world)
}

func validateAddonCheckpoint(restore, contractHash string, installedWorld []string) error {
	var checkpoint addonRestoreCheckpoint
	checkpointPath := filepath.Join(restore, "checkpoint.json")
	info, err := os.Lstat(checkpointPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 2 || info.Size() > 1<<20 {
		return errors.New("missing or unsafe addon restore checkpoint")
	}
	raw, err := os.ReadFile(checkpointPath)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&checkpoint); err != nil || decoder.Decode(&struct{}{}) != io.EOF || checkpoint.Format != 1 || checkpoint.ContractSHA256 != contractHash || !digestRE.MatchString(checkpoint.ContractSHA256) {
		return errors.New("invalid addon restore checkpoint")
	}
	if strings.Join(checkpoint.World, "\n") != strings.Join(installedWorld, "\n") {
		return errors.New("addon restore checkpoint changed installed intent")
	}
	wanted := map[string]bool{}
	for _, intent := range installedWorld {
		match := addonIntentRE.FindStringSubmatch(intent)
		if len(match) == 0 {
			return errors.New("invalid addon intent in restore checkpoint")
		}
		wanted[match[1]] = true
	}
	seenNames, seenPaths := map[string]bool{}, map[string]bool{}
	var checks strings.Builder
	var total int64
	if len(checkpoint.Packages) > 1024 {
		return errors.New("addon closure contains too many packages")
	}
	for _, pkg := range checkpoint.Packages {
		if !addonNameRE.MatchString(pkg.Name) || pkg.Version == "" || strings.ContainsAny(pkg.Version, "\x00\r\n/\\") ||
			!addonPackagePathRE.MatchString(pkg.Path) || !digestRE.MatchString(pkg.SHA256) || pkg.Size < 1 || pkg.Size > 64<<20 || seenNames[pkg.Name] || seenPaths[pkg.Path] {
			return errors.New("invalid addon closure entry")
		}
		seenNames[pkg.Name], seenPaths[pkg.Path] = true, true
		total += pkg.Size
		if total > 256<<20 {
			return errors.New("addon closure is too large")
		}
		name := filepath.Join(restore, filepath.FromSlash(pkg.Path))
		info, statErr := os.Lstat(name)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != pkg.Size {
			return errors.New("missing or unsafe addon closure package")
		}
		if got, hashErr := HashFile(name); hashErr != nil || got != pkg.SHA256 {
			return errors.New("addon closure package checksum mismatch")
		}
		fmt.Fprintf(&checks, "%s  %s\n", pkg.SHA256, pkg.Path)
	}
	for name := range wanted {
		if !seenNames[name] {
			return fmt.Errorf("target repository does not support installed addon %s; remove it before updating", name)
		}
	}
	return writeAtomic(filepath.Join(restore, "packages.sha256"), []byte(checks.String()), 0600)
}
