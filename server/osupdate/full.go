package osupdate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const FullUpdaterVersion = 2
const FullPlatform = "sg2002-sd-v2"
const FullRootFSBytes int64 = 1560281088
const fullStage = "/data/.nkos-full-update"

// Format 4 carries a complete rootfs and a signed RAM installer. Compatibility
// depends on the updater/platform contract, not the source release or library ABI.
type FullSystemUpdate struct {
	Platform              string `json:"platform"`
	MinimumUpdater        uint32 `json:"minimum_updater"`
	TargetUpdater         uint32 `json:"target_updater,omitempty"`
	TargetUpdaterSequence uint64 `json:"target_updater_sequence,omitempty"`
	TargetUpdaterSHA256   string `json:"target_updater_sha256,omitempty"`
	AddonContractSHA256   string `json:"addon_contract_sha256,omitempty"`
	RootFSBytes           int64  `json:"rootfs_bytes"`
	RootFSSHA256          string `json:"rootfs_sha256"`
	BootloaderSHA256      string `json:"bootloader_sha256"`
	KernelRelease         string `json:"kernel_release"`
}

func validateFullManifest(m Manifest) error {
	if m.Format != 4 || m.Kind != "full-system" || m.Product != "NanoKVM OS" || m.Arch != "riscv64" ||
		!versionRE.MatchString(m.Version) || m.Sequence == 0 || m.Full == nil || m.Updater != nil || m.Kernel != nil ||
		m.NativeABI != "" || m.SystemBase != "" || len(m.Remove) != 0 ||
		!digestRE.MatchString(m.PayloadSHA256) || m.PayloadBytes < 1 || m.PayloadBytes > MaxBundle {
		return errors.New("invalid full-system manifest")
	}
	f := m.Full
	if f.Platform != FullPlatform || f.MinimumUpdater < 1 || f.RootFSBytes != FullRootFSBytes ||
		!digestRE.MatchString(f.RootFSSHA256) || !digestRE.MatchString(f.BootloaderSHA256) || !kernelReleaseRE.MatchString(f.KernelRelease) {
		return errors.New("unsupported full-system platform or layout; use the full SD image")
	}
	if (f.TargetUpdater == 0 && (f.MinimumUpdater != 1 || f.TargetUpdaterSequence != 0 || f.TargetUpdaterSHA256 != "")) ||
		(f.TargetUpdater > 0 && (f.TargetUpdater < updaterFormatCapability || !digestRE.MatchString(f.TargetUpdaterSHA256))) {
		return errors.New("invalid full-system updater transition")
	}
	legacy := f.AddonContractSHA256 == ""
	if (!legacy && (!digestRE.MatchString(f.AddonContractSHA256) || f.MinimumUpdater < updaterFormatCapability)) || (legacy && f.TargetUpdater != 0) {
		return errors.New("invalid full-system addon contract")
	}
	wantFiles := 4
	if legacy {
		wantFiles = 3
	}
	if len(m.Files) != wantFiles {
		return errors.New("full-system update has incomplete image or addon metadata")
	}
	seen := map[string]bool{}
	var total int64
	for _, e := range m.Files {
		limit := int64(16 << 20)
		switch e.Path {
		case "rootfs.ext4.gz":
			limit = MaxBundle
		case "normal-boot.sd", "ram-update.sd":
		case "addons-contract.json":
			if legacy {
				return errors.New("legacy full-system update cannot carry addon metadata")
			}
			limit = 1 << 20
		default:
			return errors.New("unexpected full-system payload")
		}
		if seen[e.Path] || e.Size < 40 || e.Size > limit || e.Mode != 0644 || e.Link != "" || e.Preserve || !digestRE.MatchString(e.SHA256) {
			return errors.New("invalid full-system image entry")
		}
		seen[e.Path] = true
		total += e.Size
	}
	if !legacy && fullImageEntry(m, "addons-contract.json").SHA256 != f.AddonContractSHA256 {
		return errors.New("addon contract digest mismatch")
	}
	if total > MaxExpanded {
		return errors.New("full-system payload too large")
	}
	return nil
}

type fullUpdater struct {
	root string
	lock *os.File
}

func newFullUpdater(lock ...*os.File) *fullUpdater {
	u := &fullUpdater{root: "/"}
	if len(lock) > 0 {
		u.lock = lock[0]
	}
	return u
}
func (u *fullUpdater) at(p string) string { return filepath.Join(u.root, p) }
func (u *fullUpdater) check(m Manifest) error {
	if err := validateFullManifest(m); err != nil {
		return err
	}
	if m.Full.MinimumUpdater > FullUpdaterVersion {
		return errors.New("this package needs a newer updater; install the full SD image")
	}
	current, err := readUpdaterAt(u.at(Base))
	if err != nil {
		return err
	}
	targetUpdater := m.Full.TargetUpdater
	if targetUpdater == 0 {
		targetUpdater = 1 // Legacy format-4 packages predate explicit target metadata.
	}
	if targetUpdater < current.Capability {
		return errors.New("complete image contains an older updater than the installed helper; use a newer complete package or full SD image")
	}
	if targetUpdater == current.Capability && m.Full.TargetUpdaterSequence < current.Sequence {
		return errors.New("complete image would revert a newer independently installed updater; use a newer complete package or full SD image")
	}
	if m.Full.TargetUpdater != 0 && targetUpdater == current.Capability && m.Full.TargetUpdaterSequence == current.Sequence {
		currentHash, hashErr := HashFile(u.at(Helper))
		if hashErr != nil || currentHash != m.Full.TargetUpdaterSHA256 {
			return errors.New("complete image has a different updater at the installed monotonic sequence; use a newer complete package or full SD image")
		}
	}
	world, err := readAddonWorld(u.root)
	if err != nil {
		return err
	}
	if m.Full.AddonContractSHA256 == "" && len(world) != 0 {
		return errors.New("legacy complete package cannot preserve installed addons; remove them or use a package with addon migration metadata")
	}
	marker, err := os.ReadFile(u.at("/etc/nanokvm-buildroot"))
	if err != nil || !strings.Contains(string(marker), "flavour=enhanced") {
		return errors.New("NanoKVM OS image required")
	}
	for path, want := range map[string]string{
		"/sys/class/block/mmcblk0p1/start": "1", "/sys/class/block/mmcblk0p1/size": "131072",
		"/sys/class/block/mmcblk0p2/start": "131073", "/sys/class/block/mmcblk0p2/size": "3047424",
		"/sys/class/block/mmcblk0p3/start": "3180544",
	} {
		got, e := os.ReadFile(u.at(path))
		if e != nil || strings.TrimSpace(string(got)) != want {
			return errors.New("unsupported SD partition layout; use the full image")
		}
	}
	for path, fsType := range map[string]int64{"/boot": 0x4d44, "/data": 0x2011BAB0} {
		info, e := os.Lstat(u.at(path))
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid %s mount", path)
		}
		var st syscall.Statfs_t
		if e = syscall.Statfs(u.at(path), &st); e != nil {
			return e
		}
		if int64(st.Type) != fsType || st.Flags&1 != 0 {
			return fmt.Errorf("%s requires its writable SD filesystem", path)
		}
		need := int64(64 << 10)
		for _, entry := range m.Files {
			if path == "/data" || entry.Path == "ram-update.sd" {
				need += entry.Size
			}
		}
		if uint64(need) > st.Bavail*uint64(st.Bsize) {
			return fmt.Errorf("insufficient free space on %s", path)
		}
	}
	got, e := HashFile(u.at("/boot/fip.bin"))
	if e != nil || got != m.Full.BootloaderSHA256 {
		return errors.New("bootloader compatibility requires the full SD image")
	}
	for _, path := range []string{Base + "/system.json", Base + "/transaction.json", fullStage + "/pending"} {
		if exists(u.at(path)) {
			return errors.New("another update requires completion or recovery")
		}
	}
	return nil
}
func fullImageEntry(m Manifest, name string) Entry {
	for _, e := range m.Files {
		if e.Path == name {
			return e
		}
	}
	return Entry{}
}
func (u *fullUpdater) install(file string, b *Bundle) (err error) {
	if err := u.check(b.Manifest); err != nil {
		return err
	}
	if err := SetResult(Result{State: "installing", Version: b.Manifest.Version, ID: b.ID, Reboot: true, Message: "Preparing complete system image"}); err != nil {
		return err
	}
	// A root-owned, fixed staging directory lives outside the replaced rootfs.
	dir := u.at(fullStage)
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe full-system staging directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err = os.Mkdir(dir, 0700); err != nil {
		return err
	}
	stage := filepath.Join(dir, fullImageEntry(b.Manifest, "rootfs.ext4.gz").SHA256)
	if exists(stage) {
		info, e := os.Lstat(stage)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe existing image staging directory")
		}
		if e = os.RemoveAll(stage); e != nil {
			return e
		}
	}
	published := false
	defer func() {
		if err != nil && !published {
			_ = os.RemoveAll(stage)
			_ = os.Remove(u.at("/boot/boot.sd.nkos-next"))
			_ = os.Remove(filepath.Join(dir, "pending"))
		}
	}()
	if err := Extract(file, b, stage); err != nil {
		return err
	}
	if b.Manifest.Full.AddonContractSHA256 != "" {
		if err := SetResult(Result{State: "installing", Version: b.Manifest.Version, ID: b.ID, Reboot: true, Message: "Resolving installed addons against the target OS repository"}); err != nil {
			return err
		}
		if err := prepareAddonUpgrade(u.root, stage, b.Manifest.Full.AddonContractSHA256, u.lock); err != nil {
			return err
		}
	}
	for _, name := range []string{"normal-boot.sd", "ram-update.sd"} {
		if err := checkFIT(filepath.Join(stage, name)); err != nil {
			return err
		}
	}
	if err := syncTreeDirs(stage); err != nil {
		return err
	}
	pending := u.at("/boot/boot.sd.nkos-next")
	if err := os.Remove(pending); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := copySync(filepath.Join(stage, "ram-update.sd"), pending, 0644); err != nil {
		return err
	}
	got, err := HashFile(pending)
	if err != nil {
		return err
	}
	if got != fullImageEntry(b.Manifest, "ram-update.sd").SHA256 {
		return errors.New("RAM installer boot read-back mismatch")
	}
	// Journal before switching boot image; never write a mounted root partition.
	if err := writeJSON(filepath.Join(dir, "pending"), fullTransaction{Version: b.Manifest.Version, Sequence: b.Manifest.Sequence, ID: b.ID, Stage: fullImageEntry(b.Manifest, "rootfs.ext4.gz").SHA256, Kernel: b.Manifest.Full.KernelRelease}); err != nil {
		return err
	}
	if err := os.Rename(pending, u.at("/boot/boot.sd")); err != nil {
		_ = os.Remove(filepath.Join(dir, "pending"))
		return err
	}
	published = true
	if err := syncDir(u.at("/boot")); err != nil {
		return err
	}
	if err := SetResult(Result{State: "installing", Version: b.Manifest.Version, ID: b.ID, Reboot: true, Message: "Restarting into complete system installation"}); err != nil {
		return err
	}
	if err := exec.Command("/sbin/reboot").Run(); err != nil {
		return fmt.Errorf("restart failed: %w", err)
	}
	return nil
}

type fullTransaction struct {
	Version  string `json:"version"`
	Sequence uint64 `json:"sequence"`
	ID       string `json:"id"`
	Stage    string `json:"stage"`
	Kernel   string `json:"kernel"`
}

func HasFullUpdate() bool { return exists(fullStage + "/pending") }
func ConfirmFullSystem() (err error) {
	var tx fullTransaction
	defer func() {
		if err != nil {
			_ = SetResult(Result{State: "failed", Version: tx.Version, ID: tx.ID, Message: "Complete system update requires recovery: " + err.Error()})
		}
	}()
	if err = readJSON(fullStage+"/pending", &tx); err != nil {
		return err
	}
	if !digestRE.MatchString(tx.Stage) || !digestRE.MatchString(tx.ID) || !versionRE.MatchString(tx.Version) {
		return errors.New("invalid full-system journal")
	}
	stage := filepath.Join(fullStage, tx.Stage)
	if !exists(stage + "/COMPLETE") {
		return errors.New("full-system installation is incomplete; recovery required")
	}
	installed := GetInstalled()
	kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return err
	}
	if installed.Version != tx.Version || installed.Sequence != tx.Sequence || strings.TrimSpace(string(kernel)) != tx.Kernel {
		return errors.New("unexpected system image booted")
	}
	successes := 0
	for n := 0; n < 90; n++ {
		if healthy() {
			successes++
		} else {
			successes = 0
		}
		if successes >= 5 {
			break
		}
		time.Sleep(time.Second)
	}
	if successes < 5 {
		return errors.New("new application did not become healthy")
	}
	if err := SetResult(Result{State: "installed", Version: tx.Version, ID: tx.ID, Message: "Complete system update installed"}); err != nil {
		return err
	}
	if err := os.Remove(fullStage + "/pending"); err != nil {
		return err
	}
	if err := syncDir(fullStage); err != nil {
		return err
	}
	// Only the validated private image staging directory, never user data.
	return os.RemoveAll(stage)
}
