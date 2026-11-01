package osupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func noticeFixture(t *testing.T, repositories string) *[][]string {
	t.Helper()
	oldDir, oldRepos, oldCache, oldDisabled, oldApk, oldNow := apkStateDir, repositoriesPath, noticeCacheDir, noticeDisabledPath, noticeApk, noticeNow
	oldLists := repositoriesDir
	t.Cleanup(func() {
		apkStateDir, repositoriesPath, noticeCacheDir, noticeDisabledPath, noticeApk, noticeNow = oldDir, oldRepos, oldCache, oldDisabled, oldApk, oldNow
		repositoriesDir = oldLists
	})
	dir := t.TempDir()
	apkStateDir = filepath.Join(dir, "state")
	repositoriesPath = filepath.Join(dir, "repositories")
	repositoriesDir = filepath.Join(dir, "repositories.d")
	noticeCacheDir = filepath.Join(dir, "cache")
	noticeDisabledPath = filepath.Join(dir, "update-check.disabled")
	if err := os.WriteFile(repositoriesPath, []byte(repositories), 0644); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	noticeApk = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, args)
		return "", nil
	}
	return &calls
}

const noticeRepositories = `# comment
https://dormancygrace.github.io/NanoKVM-OS/repo/riscv64/Packages.adb
https://nkos.pesin.pro/repos/c906-qualified/riscv64/Packages.adb
https://dl-cdn.alpinelinux.org/alpine/v3.24/main
@edgecommunity https://dl-cdn.alpinelinux.org/alpine/edge/community
@nk https://example.test/nanokvm-tagged
`

func TestNanokvmRepositories(t *testing.T) {
	noticeFixture(t, noticeRepositories)
	repos, err := nanokvmRepositories()
	want := []string{"https://dormancygrace.github.io/NanoKVM-OS/repo/riscv64/Packages.adb"}
	if err != nil || !reflect.DeepEqual(repos, want) {
		t.Fatalf("repos=%q err=%v", repos, err)
	}
	// The add-on repository comes from a list file of nanokvm-base; a file
	// that does not end in .list is not read.
	if err := os.MkdirAll(repositoriesDir, 0755); err != nil {
		t.Fatal(err)
	}
	addons := "https://dormancygrace.github.io/NanoKVM-OS/addons/v3.24/riscv64/Packages.adb"
	for name, data := range map[string]string{"nanokvm-addons.list": addons + "\n",
		"alpine.list":      "https://dl-cdn.alpinelinux.org/alpine/v3.24/community",
		"old.list.apk-new": "https://example.test/nanokvm-ignored"} {
		if err := os.WriteFile(filepath.Join(repositoriesDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	repos, err = nanokvmRepositories()
	if want = append(want, addons); err != nil || !reflect.DeepEqual(repos, want) {
		t.Fatalf("with lists: repos=%q err=%v", repos, err)
	}
}

func TestCheckUpdates(t *testing.T) {
	calls := noticeFixture(t, noticeRepositories)
	now := time.Unix(1_800_000_000, 0)
	noticeNow = func() time.Time { return now }
	versions := "Installed:                                Available:\nnanokvm-app-3.0_alpha1-r0               < 3.1.0-r0 \n"
	failUpdate := false
	noticeApk = func(_ context.Context, args ...string) (string, error) {
		*calls = append(*calls, args)
		switch args[len(args)-1] {
		case "update":
			if failUpdate {
				return "ERROR: network unreachable", errors.New("exit status 1")
			}
			return "OK", nil
		case "<":
			return versions, nil
		case "nanokvm-*":
			return addonIndexFixture, nil
		}
		return "", errors.New("unexpected")
	}

	// Without a first refresh there is nothing to compare with.
	if err := CheckUpdates(context.Background(), false); err != nil || len(*calls) != 0 {
		t.Fatalf("offline first check ran %q: %v", *calls, err)
	}

	if err := CheckUpdates(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	n := GetNotice()
	if n.State != "ok" || n.CheckedAt != now.Unix() || n.AttemptAt != now.Unix() || !n.Enabled ||
		!reflect.DeepEqual(n.Updates, []SoftwareUpdate{{Name: "nanokvm-app", Installed: "3.0_alpha1-r0", Available: "3.1.0-r0"}}) {
		t.Fatalf("notice=%+v", n)
	}
	repo := "--repository=https://dormancygrace.github.io/NanoKVM-OS/repo/riscv64/Packages.adb"
	wantUpdate := []string{"--repositories-file=/dev/null", "--cache-dir=" + noticeCacheDir, repo, "update"}
	wantVersion := []string{"--repositories-file=/dev/null", "--cache-dir=" + noticeCacheDir, repo, "--no-network", "version", "-l", "<"}
	wantQuery := []string{"--repositories-file=/dev/null", "--cache-dir=" + noticeCacheDir, repo, "--no-network", "query",
		"--format=json", "--fields=name,version,description,url,license,depends", "--from=repositories", "nanokvm-*"}
	if !reflect.DeepEqual(*calls, [][]string{wantUpdate, wantVersion, wantQuery}) {
		t.Fatalf("calls=%q", *calls)
	}
	if _, err := os.Stat(noticeCacheDir); err != nil {
		t.Fatal("no cache directory for apk")
	}
	// The same check stored the add-ons of the index.
	if stored, ok := readCatalog(); !ok || stored.CheckedAt != now.Unix() || len(stored.Addons) != 2 {
		t.Fatalf("add-on list: %+v", stored)
	}

	// After an upgrade, the offline comparison clears the notice.
	*calls, versions = nil, "Installed:                                Available:\n"
	if err := CheckUpdates(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n = GetNotice(); len(n.Updates) != 0 || n.CheckedAt != now.Unix() || !reflect.DeepEqual(*calls, [][]string{wantVersion, wantQuery}) {
		t.Fatalf("offline: notice=%+v calls=%q", n, *calls)
	}

	// A failed refresh keeps what was known and records the attempt.
	versions = "nanokvm-web-3.0-r0 < 3.1-r0\n"
	_ = CheckUpdates(context.Background(), true)
	later := now.Add(2 * time.Hour)
	noticeNow = func() time.Time { return later }
	failUpdate = true
	if err := CheckUpdates(context.Background(), true); err == nil {
		t.Fatal("failed refresh reported success")
	}
	n = GetNotice()
	if n.State != "failed" || n.CheckedAt != now.Unix() || n.AttemptAt != later.Unix() || len(n.Updates) != 1 ||
		!strings.Contains(n.Message, "network unreachable") {
		t.Fatalf("failed: %+v", n)
	}

	// No NanoKVM repository: nothing to check.
	if err := os.WriteFile(repositoriesPath, []byte("https://dl-cdn.alpinelinux.org/alpine/v3.24/main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	*calls = nil
	_ = CheckUpdates(context.Background(), true)
	if n = GetNotice(); n.State != "unavailable" || len(n.Updates) != 0 || len(*calls) != 0 {
		t.Fatalf("unavailable: %+v %q", n, *calls)
	}
}

func TestNoticeDue(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	at := func(d time.Duration) int64 { return now.Add(-d).Unix() }
	for _, c := range []struct {
		name string
		n    UpdateNotice
		due  bool
	}{
		{"never checked", UpdateNotice{Enabled: true}, true},
		{"disabled", UpdateNotice{}, false},
		{"checked today", UpdateNotice{Enabled: true, CheckedAt: at(3 * time.Hour), AttemptAt: at(3 * time.Hour)}, false},
		{"a day old", UpdateNotice{Enabled: true, CheckedAt: at(25 * time.Hour), AttemptAt: at(25 * time.Hour)}, true},
		{"failed just now", UpdateNotice{Enabled: true, AttemptAt: at(10 * time.Minute)}, false},
		{"failed an hour ago", UpdateNotice{Enabled: true, AttemptAt: at(61 * time.Minute)}, true},
		{"comparison failed after fresh index", UpdateNotice{Enabled: true, State: "failed", CheckedAt: at(61 * time.Minute), AttemptAt: at(61 * time.Minute)}, true},
		{"recent failure after success", UpdateNotice{Enabled: true, State: "failed", CheckedAt: at(3 * time.Hour), AttemptAt: at(10 * time.Minute)}, false},
	} {
		if got := noticeDue(c.n, now); got != c.due {
			t.Errorf("%s: due=%v", c.name, got)
		}
	}
}
