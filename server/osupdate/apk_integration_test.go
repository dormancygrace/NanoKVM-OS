package osupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"NanoKVM-Server/internal/apkrun"
)

// tests/test-package-update-selections.py prepares real APK repositories and a
// disposable root. No host package database, package scripts or devices are used.
func TestAPKSelectionsIntegration(t *testing.T) {
	root := os.Getenv("NKOS_APK_TEST_ROOT")
	if root == "" {
		t.Skip("requires disposable native APK fixture")
	}
	var expected struct {
		Upgrades  []string          `json:"upgrades"`
		Installed map[string]string `json:"installed"`
		World     []string          `json:"world"`
	}
	data, err := os.ReadFile(filepath.Join(root, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	oldDir, oldExec, oldWorld, oldDB := apkStateDir, apkExecutable, worldPath, installedDBPath
	oldRepos, oldRepoDir, oldLock := repositoriesPath, repositoriesDir, apkrun.LockPath
	t.Cleanup(func() {
		apkStateDir, apkExecutable, worldPath, installedDBPath = oldDir, oldExec, oldWorld, oldDB
		repositoriesPath, repositoriesDir, apkrun.LockPath = oldRepos, oldRepoDir, oldLock
	})
	apkStateDir, apkExecutable = filepath.Join(root, "state"), filepath.Join(root, "apk-wrapper")
	worldPath, installedDBPath = filepath.Join(root, "etc/apk/world"), filepath.Join(root, "lib/apk/db/installed")
	repositoriesPath, repositoriesDir = filepath.Join(root, "etc/apk/repositories"), filepath.Join(root, "etc/apk/repositories.d")
	apkrun.LockPath = filepath.Join(root, "apk.lock")
	beforeWorld, err := os.ReadFile(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeDB, err := os.ReadFile(installedDBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunAPK("check"); err != nil {
		t.Fatal(err)
	}
	status := GetAPKStatus()
	var got []string
	for _, line := range strings.Split(status.Upgrades, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			got = append(got, line)
		}
	}
	slices.Sort(got)
	slices.Sort(expected.Upgrades)
	if !slices.Equal(got, expected.Upgrades) {
		t.Fatalf("offered %v, want %v", got, expected.Upgrades)
	}
	wantState := "ready"
	if len(got) == 0 {
		wantState = "up-to-date"
	}
	if status.State != wantState {
		t.Fatalf("state %s, want %s", status.State, wantState)
	}
	afterWorld, err := os.ReadFile(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	afterDB, err := os.ReadFile(installedDBPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeWorld) != string(afterWorld) || string(beforeDB) != string(afterDB) {
		t.Fatal("check modified world or installed packages")
	}
	if err := RunAPK("install"); err != nil {
		t.Fatal(err)
	}
	installed, err := installedVersions()
	if err != nil {
		t.Fatal(err)
	}
	for name, version := range expected.Installed {
		if installed[name] != version {
			t.Fatalf("installed %s=%s, want %s", name, installed[name], version)
		}
	}
	world, err := os.ReadFile(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	entries := strings.Fields(string(world))
	slices.Sort(entries)
	slices.Sort(expected.World)
	if !slices.Equal(entries, expected.World) {
		t.Fatalf("world %v, want %v", entries, expected.World)
	}
}
