package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func updaterFixture() Manifest {
	digest := strings.Repeat("a", 64)
	return Manifest{
		Format: 5, Kind: "updater", Product: "NanoKVM OS", Arch: "riscv64", Version: "1.0.0-beta.6", Sequence: 2,
		Updater: &UpdaterUpdate{Capability: 2, MinimumSource: 2}, PayloadBytes: 8192, PayloadSHA256: digest,
		Files: []Entry{{Path: "nkos-update", Size: 8192, Mode: 0755, SHA256: digest}},
	}
}

func TestUpdaterManifestBoundaries(t *testing.T) {
	if err := validateManifest(updaterFixture()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Manifest){
		"wrong kind":       func(m *Manifest) { m.Kind = "system" },
		"old source":       func(m *Manifest) { m.Updater.MinimumSource = 1 },
		"future source":    func(m *Manifest) { m.Updater.MinimumSource = 3 },
		"wrong executable": func(m *Manifest) { m.Files[0].Path = "other" },
		"dynamic metadata": func(m *Manifest) { m.Full = &FullSystemUpdate{} },
		"source ABI":       func(m *Manifest) { m.NativeABI = strings.Repeat("b", 64) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := updaterFixture()
			mutate(&m)
			if validateManifest(m) == nil {
				t.Fatal("accepted invalid updater package")
			}
		})
	}
}

func TestUpdaterStateKeepsHistoricalCapabilitiesAndFailsClosed(t *testing.T) {
	if !validUpdaterState(UpdaterState{Capability: 1}) || !validUpdaterState(UpdaterState{Capability: UpdaterCapability + 1}) {
		t.Fatal("capability evolution rejected a valid historical state")
	}
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "updater.json"), []byte(`{"capability":2,"sequence":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdaterAt(base); err == nil {
		t.Fatal("corrupt monotonic state reset to a replayable default")
	}
}

func TestPublishedHelperDoesNotResetMissingMonotonicState(t *testing.T) {
	old := UpdaterBuildSequence
	UpdaterBuildSequence = "9"
	defer func() { UpdaterBuildSequence = old }()
	if _, err := readUpdaterAt(t.TempDir()); err == nil || !strings.Contains(err.Error(), "monotonic state is missing") {
		t.Fatalf("published helper reset missing state: %v", err)
	}
}

func writeTestFile(t *testing.T, name, data string) string {
	t.Helper()
	if err := os.WriteFile(name, []byte(data), 0755); err != nil {
		t.Fatal(err)
	}
	sum, err := HashFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestRecoverUpdaterCompletesPublishedReplacement(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "state")
	helper := filepath.Join(root, "nkos-update")
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	oldHash := writeTestFile(t, helper+".nkos-previous", "old updater")
	newHash := writeTestFile(t, helper, "new updater")
	oldState := UpdaterState{Capability: 2, Version: "1.0.0-beta.5", Sequence: 1, ID: strings.Repeat("1", 64)}
	newState := UpdaterState{Capability: 2, Version: "1.0.0-beta.6", Sequence: 2, ID: strings.Repeat("2", 64)}
	journal, previous := updaterRecoveryPaths(base, helper)
	if err := writeJSON(journal, updaterTransaction{Phase: "publishing", Old: oldState, New: newState, OldHash: oldHash, NewHash: newHash}); err != nil {
		t.Fatal(err)
	}
	if err := recoverUpdaterAt(base, helper); err != nil {
		t.Fatal(err)
	}
	if got := getUpdaterAt(base); got != newState {
		t.Fatalf("wrong recovered state: %#v", got)
	}
	if exists(journal) || exists(previous) {
		t.Fatal("recovery artifacts retained")
	}
}

func TestRecoverUpdaterRestoresRecoveryCopy(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "state")
	helper := filepath.Join(root, "nkos-update")
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	oldHash := writeTestFile(t, helper+".nkos-previous", "old updater")
	_ = writeTestFile(t, helper, "corrupt replacement")
	oldState := UpdaterState{Capability: 2}
	newState := UpdaterState{Capability: 2, Version: "1.0.0-beta.6", Sequence: 2, ID: strings.Repeat("2", 64)}
	journal, _ := updaterRecoveryPaths(base, helper)
	if err := writeJSON(journal, updaterTransaction{Phase: "publishing", Old: oldState, New: newState, OldHash: oldHash, NewHash: strings.Repeat("3", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := recoverUpdaterAt(base, helper); err != nil {
		t.Fatal(err)
	}
	if got, _ := HashFile(helper); got != oldHash {
		t.Fatal("old updater was not restored")
	}
}
