package osupdate

import (
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// UpdaterCapability is the independently replaceable parser/installer API.
// Capability 2 moves package preparation behind the helper and adds format 5.
const UpdaterCapability uint32 = 2
const updaterFormatCapability uint32 = 2

// Set by -ldflags for independently distributed updater binaries. A helper
// with a published sequence refuses to recreate missing monotonic state.
var UpdaterBuildVersion string
var UpdaterBuildSequence string

type UpdaterUpdate struct {
	Capability    uint32 `json:"capability"`
	MinimumSource uint32 `json:"minimum_source"`
}

type UpdaterState struct {
	Capability uint32 `json:"capability"`
	Version    string `json:"version,omitempty"`
	Sequence   uint64 `json:"sequence"`
	ID         string `json:"id,omitempty"`
}

type UpdaterSelfTest struct {
	Protocol      uint32 `json:"protocol"`
	Capability    uint32 `json:"capability"`
	BuildVersion  string `json:"build_version,omitempty"`
	BuildSequence uint64 `json:"build_sequence,omitempty"`
}

type updaterTransaction struct {
	Phase   string       `json:"phase"`
	Old     UpdaterState `json:"old"`
	New     UpdaterState `json:"new"`
	OldHash string       `json:"old_hash"`
	NewHash string       `json:"new_hash"`
}

func defaultUpdaterState() UpdaterState { return UpdaterState{Capability: UpdaterCapability} }

func validUpdaterState(s UpdaterState) bool {
	if s.Capability < 1 {
		return false
	}
	if s.Sequence == 0 {
		return s.ID == "" && s.Version == ""
	}
	return digestRE.MatchString(s.ID) && versionRE.MatchString(s.Version)
}

func GetUpdater() UpdaterState {
	state, _ := readUpdaterAt(Base)
	return state
}

func getUpdaterAt(base string) UpdaterState {
	state, _ := readUpdaterAt(base)
	return state
}

func readUpdaterAt(base string) (UpdaterState, error) {
	var saved UpdaterState
	err := readJSON(base+"/updater.json", &saved)
	if os.IsNotExist(err) {
		if sequence, parseErr := strconv.ParseUint(UpdaterBuildSequence, 10, 64); parseErr == nil && sequence > 0 {
			return UpdaterState{}, errors.New("updater monotonic state is missing; recover it or reinstall from a full image")
		}
		return defaultUpdaterState(), nil
	}
	if err != nil || !validUpdaterState(saved) {
		return UpdaterState{}, errors.New("updater state is missing or corrupt; reinstall from a full image")
	}
	return saved, nil
}

func validateUpdaterManifest(m Manifest) error {
	if m.Format != 5 || m.Kind != "updater" || m.Product != "NanoKVM OS" || m.Arch != "riscv64" ||
		!versionRE.MatchString(m.Version) || m.Sequence == 0 || m.Updater == nil || m.Full != nil || m.Kernel != nil ||
		m.NativeABI != "" || m.SystemBase != "" || len(m.Remove) != 0 ||
		!digestRE.MatchString(m.PayloadSHA256) || m.PayloadBytes < 1 || m.PayloadBytes > 32<<20 {
		return errors.New("invalid updater manifest")
	}
	if m.Updater.Capability < updaterFormatCapability || m.Updater.MinimumSource < updaterFormatCapability || m.Updater.MinimumSource > m.Updater.Capability {
		return errors.New("invalid updater capability transition")
	}
	if len(m.Files) != 1 {
		return errors.New("updater package requires exactly one file")
	}
	e := m.Files[0]
	if e.Path != "nkos-update" || e.Size < 4096 || e.Size > 32<<20 || e.Mode != 0755 ||
		e.Link != "" || e.Preserve || !digestRE.MatchString(e.SHA256) {
		return errors.New("invalid updater executable entry")
	}
	return nil
}

func checkUpdaterUpdate(m Manifest) error {
	if err := validateUpdaterManifest(m); err != nil {
		return err
	}
	current, err := readUpdaterAt(Base)
	if err != nil {
		return err
	}
	if m.Updater.MinimumSource > current.Capability {
		return errors.New("this updater package needs a newer bootstrap updater")
	}
	if m.Updater.Capability < current.Capability || (m.Updater.Capability == current.Capability && m.Sequence <= current.Sequence) {
		return errors.New("this updater is already installed or older")
	}
	return nil
}

func ValidateStaticUpdater(name string) error {
	f, err := elf.Open(name)
	if err != nil {
		return fmt.Errorf("invalid updater ELF: %w", err)
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != elf.EM_RISCV || f.Type != elf.ET_EXEC {
		return errors.New("updater is not a static RISC-V ELF64 executable")
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
			return errors.New("updater must not depend on the rootfs dynamic loader")
		}
	}
	return nil
}

func updaterRecoveryPaths(base, helper string) (journal, previous string) {
	return base + "/updater-transaction.json", helper + ".nkos-previous"
}

// RecoverUpdater completes a published replacement or restores the old helper.
// It is called under the canonical update lock before every mutating command.
func RecoverUpdater() error {
	return recoverUpdaterAt(Base, Helper)
}

func recoverUpdaterAt(base, helper string) error {
	journal, previous := updaterRecoveryPaths(base, helper)
	var tx updaterTransaction
	if err := readJSON(journal, &tx); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if tx.Phase != "publishing" || !validUpdaterState(tx.Old) || !validUpdaterState(tx.New) ||
		!digestRE.MatchString(tx.OldHash) || !digestRE.MatchString(tx.NewHash) {
		return errors.New("invalid updater recovery journal")
	}
	got, err := HashFile(helper)
	installed := false
	if err == nil && got == tx.NewHash {
		if err = writeJSON(base+"/updater.json", tx.New); err != nil {
			return err
		}
		installed = true
	} else if err == nil && got == tx.OldHash {
		if err = writeJSON(base+"/updater.json", tx.Old); err != nil {
			return err
		}
	} else {
		backupHash, backupErr := HashFile(previous)
		if backupErr != nil || backupHash != tx.OldHash {
			return errors.New("updater replacement interrupted and recovery copy is invalid")
		}
		if err = os.Rename(previous, helper); err != nil {
			return err
		}
		if err = syncDir(filepath.Dir(helper)); err != nil {
			return err
		}
		if err = writeJSON(base+"/updater.json", tx.Old); err != nil {
			return err
		}
	}
	result := Result{State: "rolled-back", Version: tx.Old.Version, ID: tx.Old.ID, Message: "Interrupted updater replacement restored the previous helper"}
	if installed {
		result = Result{State: "installed", Version: tx.New.Version, ID: tx.New.ID, Message: "Independent updater installed"}
	}
	if err = writeJSON(base+"/result.json", result); err != nil {
		return err
	}
	if err = os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = os.Remove(journal); err != nil {
		return err
	}
	return syncDir(base)
}

func installUpdater(file string, b *Bundle) (err error) {
	if err = checkUpdaterUpdate(b.Manifest); err != nil {
		return err
	}
	stage := Base + "/updater-next"
	if err = os.RemoveAll(stage); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = Extract(file, b, stage); err != nil {
		return err
	}
	candidate := filepath.Join(stage, "nkos-update")
	if err = ValidateStaticUpdater(candidate); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, runErr := exec.CommandContext(ctx, candidate, "self-test").Output()
	var self UpdaterSelfTest
	if runErr != nil || json.Unmarshal(output, &self) != nil || self.Protocol != 1 || self.Capability != b.Manifest.Updater.Capability ||
		self.BuildVersion != b.Manifest.Version || self.BuildSequence != b.Manifest.Sequence {
		return errors.New("new updater self-test failed or reported the wrong capability")
	}
	next := Helper + ".nkos-next"
	if err = os.Remove(next); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = copySync(candidate, next, 0755); err != nil {
		return err
	}
	newHash := b.Manifest.Files[0].SHA256
	if got, hashErr := HashFile(next); hashErr != nil || got != newHash {
		_ = os.Remove(next)
		return errors.New("updater read-back mismatch")
	}
	oldHash, err := HashFile(Helper)
	if err != nil {
		return err
	}
	journal, previous := updaterRecoveryPaths(Base, Helper)
	if err = os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = os.Link(Helper, previous); err != nil {
		return fmt.Errorf("create updater recovery copy: %w", err)
	}
	if err = syncDir(filepath.Dir(Helper)); err != nil {
		return err
	}
	state := UpdaterState{Capability: b.Manifest.Updater.Capability, Version: b.Manifest.Version, Sequence: b.Manifest.Sequence, ID: b.ID}
	oldState, err := readUpdaterAt(Base)
	if err != nil {
		return err
	}
	tx := updaterTransaction{Phase: "publishing", Old: oldState, New: state, OldHash: oldHash, NewHash: newHash}
	if err = writeJSON(journal, tx); err != nil {
		return err
	}
	if err = os.Rename(next, Helper); err != nil {
		return err
	}
	if err = syncDir(filepath.Dir(Helper)); err != nil {
		return err
	}
	return RecoverUpdater()
}
