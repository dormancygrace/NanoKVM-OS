package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareAddonUpgradeWritesExplicitEmptyCheckpoint(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	contract := strings.Repeat("a", 64)
	if err := prepareAddonUpgrade(root, stage, contract, nil); err != nil {
		t.Fatal(err)
	}
	var checkpoint addonRestoreCheckpoint
	if err := readJSON(filepath.Join(stage, "addon-restore/checkpoint.json"), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Format != 1 || checkpoint.ContractSHA256 != contract || checkpoint.World == nil || checkpoint.Packages == nil {
		t.Fatalf("wrong empty checkpoint: %#v", checkpoint)
	}
}

func TestAddonCheckpointVerifiesExactIntentAndBytes(t *testing.T) {
	restore := t.TempDir()
	if err := os.Mkdir(filepath.Join(restore, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	pkg := []byte("signed apk fixture")
	path := filepath.Join(restore, "packages/nkos-addon-demo-1.0.apk")
	if err := os.WriteFile(path, pkg, 0600); err != nil {
		t.Fatal(err)
	}
	contract := strings.Repeat("b", 64)
	checkpoint := addonRestoreCheckpoint{Format: 1, ContractSHA256: contract, World: []string{"nkos-addon-demo"}, Packages: []addonRestorePackage{{Name: "nkos-addon-demo", Version: "1.0-r0", Path: "packages/nkos-addon-demo-1.0.apk", Size: int64(len(pkg)), SHA256: digest(pkg)}}}
	if err := writeJSON(filepath.Join(restore, "checkpoint.json"), checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := validateAddonCheckpoint(restore, contract, []string{"nkos-addon-demo"}); err != nil {
		t.Fatal(err)
	}
	checkpoint.Packages[0].SHA256 = strings.Repeat("c", 64)
	if err := writeJSON(filepath.Join(restore, "checkpoint.json"), checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := validateAddonCheckpoint(restore, contract, []string{"nkos-addon-demo"}); err == nil {
		t.Fatal("accepted wrong cached package checksum")
	}
}

func TestPrepareAddonUpgradeUsesTargetContractAndCanonicalLock(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	worldDir := filepath.Join(root, "opt/nkos/etc/apk")
	manager := filepath.Join(root, "usr/sbin/nkos-addons")
	if err := os.MkdirAll(worldDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worldDir, "world"), []byte("nkos-addon-demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager, []byte("manager"), 0755); err != nil {
		t.Fatal(err)
	}
	contractHash := strings.Repeat("d", 64)
	lock, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	runner := func(gotManager, contract, restore string, gotLock *os.File) error {
		if gotManager != manager || contract != filepath.Join(stage, "addons-contract.json") || gotLock != lock {
			t.Fatal("wrong addon manager transport contract")
		}
		if err := os.Mkdir(filepath.Join(restore, "packages"), 0700); err != nil {
			return err
		}
		payload := []byte("apk")
		path := filepath.Join(restore, "packages/nkos-addon-demo.apk")
		if err := os.WriteFile(path, payload, 0600); err != nil {
			return err
		}
		return writeJSON(filepath.Join(restore, "checkpoint.json"), addonRestoreCheckpoint{Format: 1, ContractSHA256: contractHash, World: []string{"nkos-addon-demo"}, Packages: []addonRestorePackage{{Name: "nkos-addon-demo", Version: "1-r0", Path: "packages/nkos-addon-demo.apk", Size: int64(len(payload)), SHA256: digest(payload)}}})
	}
	if err = prepareAddonUpgradeWith(root, stage, contractHash, lock, runner); err != nil {
		t.Fatal(err)
	}
}

func TestAddonWorldPreservesVersionExpressionsAndIgnoresImageProviders(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "opt/nkos/etc/apk")
	if err := os.MkdirAll(name, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(name, "world"), []byte("nkos-base-abi=1.0\nnkos-server-api=2\nnkos-feature-video=1\nnkos-addon-demo>=1.2-r0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	world, err := readAddonWorld(root)
	if err != nil || len(world) != 1 || world[0] != "nkos-addon-demo>=1.2-r0" {
		t.Fatalf("wrong addon intent: %v, %v", world, err)
	}
}
