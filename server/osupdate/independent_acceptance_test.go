package osupdate

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests are intentionally separate from executor-owned tests. They
// exercise externally visible contracts with independent fixtures. An
// explicitly supplied beta-4 candidate can be used for optional integration.

func TestIndependentUpdaterStateHistoricalAndCorrupt(t *testing.T) {
	id := strings.Repeat("a", 64)
	valid := []UpdaterState{
		{Capability: 1},
		{Capability: 1, Version: "1.0.0-beta.4", Sequence: 10, ID: id},
		{Capability: UpdaterCapability, Version: "1.0.0-beta.6", Sequence: 20, ID: id},
		{Capability: UpdaterCapability + 50, Version: "9.9.9-future", Sequence: 1, ID: id},
	}
	for _, state := range valid {
		if !validUpdaterState(state) {
			t.Errorf("valid historical/future state rejected: %#v", state)
		}
	}
	invalid := []UpdaterState{
		{},
		{Capability: 0, Version: "1.0.0-beta.4", Sequence: 1, ID: id},
		{Capability: 1, Sequence: 1, ID: id},
		{Capability: 1, Version: "1.0.0-beta.4", Sequence: 1},
		{Capability: 1, Version: "bad version", Sequence: 1, ID: id},
		{Capability: 1, Version: "1.0.0-beta.4", Sequence: 0, ID: id},
	}
	for _, state := range invalid {
		if validUpdaterState(state) {
			t.Errorf("corrupt state accepted: %#v", state)
		}
	}

	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "updater.json"), []byte(`{"capability":2,"sequence":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUpdaterAt(base); err == nil {
		t.Fatal("incomplete monotonic state reset to a replayable default")
	}
	if _, err := readUpdaterAt(t.TempDir()); err != nil {
		t.Fatalf("missing state did not use bootstrap capability: %v", err)
	}
}

func TestIndependentUpdaterRecoveryRejectsMalformedJournalWithoutMutation(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "state")
	helper := filepath.Join(root, "nkos-update")
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("current helper"), 0755); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 64)
	old := UpdaterState{Capability: 1, Version: "1.0.0-beta.4", Sequence: 10, ID: id}
	newState := UpdaterState{Capability: 2, Version: "1.0.0-beta.6", Sequence: 20, ID: id}
	oldHash := digest([]byte("old helper"))
	newHash := digest([]byte("new helper"))
	journal, previous := updaterRecoveryPaths(base, helper)

	cases := []struct {
		name string
		tx   updaterTransaction
	}{
		{"wrong phase", updaterTransaction{Phase: "ready", Old: old, New: newState, OldHash: oldHash, NewHash: newHash}},
		{"invalid old state", updaterTransaction{Phase: "publishing", Old: UpdaterState{Capability: 1, Sequence: 1}, New: newState, OldHash: oldHash, NewHash: newHash}},
		{"invalid new state", updaterTransaction{Phase: "publishing", Old: old, New: UpdaterState{Capability: 2, Sequence: 1}, OldHash: oldHash, NewHash: newHash}},
		{"invalid hash", updaterTransaction{Phase: "publishing", Old: old, New: newState, OldHash: "bad", NewHash: newHash}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := writeJSON(journal, tc.tx); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(helper)
			if err != nil {
				t.Fatal(err)
			}
			if err := recoverUpdaterAt(base, helper); err == nil {
				t.Fatal("malformed updater journal accepted")
			}
			after, err := os.ReadFile(helper)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("malformed journal changed the helper")
			}
			if _, err := os.Stat(journal); err != nil {
				t.Fatalf("malformed journal was removed: %v", err)
			}
		})
	}

	// A replacement with no valid recovery copy must fail closed and preserve
	// the unexpected helper for diagnosis.
	if err := writeJSON(journal, updaterTransaction{Phase: "publishing", Old: old, New: newState, OldHash: oldHash, NewHash: newHash}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("corrupt replacement"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous, []byte("wrong recovery copy"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := recoverUpdaterAt(base, helper); err == nil {
		t.Fatal("invalid recovery copy accepted")
	}
	if got, _ := os.ReadFile(helper); string(got) != "corrupt replacement" {
		t.Fatalf("unexpected helper mutation after failed recovery: %q", got)
	}
}

func TestIndependentPreparedReceiptAllowsHelperEvolution(t *testing.T) {
	id := strings.Repeat("c", 64)
	raw := `{"id":"` + id + `","version":"1.0.0-beta.6","sequence":12,"reboot":true,"future_capability":7,"future_manifest":{"format":99,"files":["ignored"]}}`
	receipt, err := DecodePreparedReceipt(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ID != id || receipt.Version != "1.0.0-beta.6" || receipt.Sequence != 12 || !receipt.Reboot {
		t.Fatalf("stable receipt fields changed: %#v", receipt)
	}
}

func TestIndependentCandidateBootstrapVerifiesAndContainsStaticHelper(t *testing.T) {
	name := os.Getenv("NKOS_BETA4_BOOTSTRAP")
	if name == "" {
		t.Skip("set NKOS_BETA4_BOOTSTRAP to an explicit candidate bootstrap artifact")
	}
	if _, err := os.Stat(name); os.IsNotExist(err) {
		t.Skip("explicit candidate bootstrap artifact is unavailable")
	} else if err != nil {
		t.Fatal(err)
	}
	b, err := Verify(name)
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.Format != 2 || b.Manifest.Kind != "system" || b.Manifest.SystemBase == "" || b.Manifest.NativeABI == "" {
		t.Fatalf("unexpected candidate bootstrap contract: %#v", b.Manifest)
	}
	if err := VerifyContents(name, b); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "bootstrap")
	if err := Extract(name, b, dest); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dest, "rootfs", "usr", "sbin", "nkos-update")
	if err := ValidateStaticUpdater(helper); err != nil {
		t.Fatalf("candidate helper failed static ELF contract: %v", err)
	}
	if err := CheckExecutable(filepath.Join(dest, "NanoKVM-Server")); err != nil {
		t.Fatalf("candidate server failed RISC-V ELF contract: %v", err)
	}
}

func TestIndependentFullImageUpdaterTransitionContract(t *testing.T) {
	cases := []struct {
		name    string
		minimum uint32
		target  uint32
		valid   bool
	}{
		{"legacy beta4", 1, 0, true},
		{"explicit current helper", 2, UpdaterCapability, true},
		{"explicit future helper", 2, UpdaterCapability + 1, true},
		{"implicit target with future minimum", 2, 0, false},
		{"explicit old target with future minimum", 2, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fullFixture()
			m.Full.MinimumUpdater = tc.minimum
			m.Full.TargetUpdater = tc.target
			if tc.name == "legacy beta4" {
				m.Full.TargetUpdaterSequence = 0
				m.Full.TargetUpdaterSHA256 = ""
				m.Full.AddonContractSHA256 = ""
				m.Files = m.Files[:3]
			} else {
				m.Full.TargetUpdaterSequence = 20
				m.Full.TargetUpdaterSHA256 = strings.Repeat("a", 64)
			}
			err := validateManifest(m)
			if (err == nil) != tc.valid {
				t.Fatalf("manifest transition validity=%v, want %v; err=%v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestIndependentSameCapabilityTargetCannotRewindSequence(t *testing.T) {
	m := fullFixture()
	m.Full.TargetUpdaterSequence = 4
	root := t.TempDir()
	base := filepath.Join(root, "kvmapp/.os-update")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	current := UpdaterState{Capability: UpdaterCapability, Version: "1.0.0-beta.8", Sequence: 5, ID: strings.Repeat("b", 64)}
	if err := writeJSON(filepath.Join(base, "updater.json"), current); err != nil {
		t.Fatal(err)
	}
	if err := (&fullUpdater{root: root}).check(m); err == nil || !strings.Contains(err.Error(), "revert") {
		t.Fatalf("same-capability helper sequence rewind was accepted: %v", err)
	}
}

func TestIndependentFITBoundsRejectMalformedHeaders(t *testing.T) {
	valid := make([]byte, 64)
	binary.BigEndian.PutUint32(valid[0:4], 0xd00dfeed)
	binary.BigEndian.PutUint32(valid[4:8], 64)
	binary.BigEndian.PutUint32(valid[8:12], 40)
	binary.BigEndian.PutUint32(valid[12:16], 56)
	binary.BigEndian.PutUint32(valid[32:36], 8)
	binary.BigEndian.PutUint32(valid[36:40], 16)
	cases := []struct {
		name string
		edit func([]byte)
	}{
		{"wrong magic", func(b []byte) { binary.BigEndian.PutUint32(b[0:4], 0) }},
		{"short total", func(b []byte) { binary.BigEndian.PutUint32(b[4:8], 39) }},
		{"total beyond file", func(b []byte) { binary.BigEndian.PutUint32(b[4:8], 65) }},
		{"first block before header", func(b []byte) { binary.BigEndian.PutUint32(b[8:12], 39) }},
		{"second block zero length", func(b []byte) { binary.BigEndian.PutUint32(b[36:40], 0) }},
		{"second block outside total", func(b []byte) { binary.BigEndian.PutUint32(b[12:16], 60) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := append([]byte(nil), valid...)
			tc.edit(candidate)
			name := filepath.Join(t.TempDir(), "boot.sd")
			if err := os.WriteFile(name, candidate, 0644); err != nil {
				t.Fatal(err)
			}
			if err := checkFIT(name); err == nil {
				t.Fatal("malformed FIT header accepted")
			}
		})
	}
	name := filepath.Join(t.TempDir(), "boot.sd")
	if err := os.WriteFile(name, valid, 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkFIT(name); err != nil {
		t.Fatalf("valid FIT rejected: %v", err)
	}
}

func TestIndependentExtractionFailureRemovesStaging(t *testing.T) {
	file, key := fixture(t, nil, nil)
	b, err := verifyWithKey(file, key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if b.Offset >= int64(len(data)) {
		t.Fatal("fixture has no payload")
	}
	data[b.Offset] ^= 1
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "staging")
	if err := Extract(file, b, dest); err == nil {
		t.Fatal("corrupt payload extracted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("failed staging tree retained: %v", err)
	}
}
