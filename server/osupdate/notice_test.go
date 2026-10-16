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
	t.Cleanup(func() {
		apkStateDir, repositoriesPath, noticeCacheDir, noticeDisabledPath, noticeApk, noticeNow = oldDir, oldRepos, oldCache, oldDisabled, oldApk, oldNow
	})
	dir := t.TempDir()
	apkStateDir = filepath.Join(dir, "state")
	repositoriesPath = filepath.Join(dir, "repositories")
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
	if !reflect.DeepEqual(*calls, [][]string{wantUpdate, wantVersion}) {
		t.Fatalf("calls=%q", *calls)
	}
	if _, err := os.Stat(noticeCacheDir); err != nil {
		t.Fatal("no cache directory for apk")
	}

	// After an upgrade, the offline comparison clears the notice.
	*calls, versions = nil, "Installed:                                Available:\n"
	if err := CheckUpdates(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n = GetNotice(); len(n.Updates) != 0 || n.CheckedAt != now.Unix() || !reflect.DeepEqual(*calls, [][]string{wantVersion}) {
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
	} {
		if got := noticeDue(c.n, now); got != c.due {
			t.Errorf("%s: due=%v", c.name, got)
		}
	}
}

func TestNoticeEnabledSetting(t *testing.T) {
	noticeFixture(t, "")
	if !NoticeEnabled() {
		t.Fatal("off by default")
	}
	if err := SetNoticeEnabled(false); err != nil || NoticeEnabled() || GetNotice().Enabled {
		t.Fatalf("still on: %v", err)
	}
	if err := SetNoticeEnabled(true); err != nil || !NoticeEnabled() {
		t.Fatalf("still off: %v", err)
	}
	if err := SetNoticeEnabled(true); err != nil {
		t.Fatalf("enabling twice: %v", err)
	}
}

func TestComponents(t *testing.T) {
	noticeFixture(t, noticeRepositories)
	oldDB, oldAlpine, oldKernel, oldBoot := installedDBPath, alpineReleasePath, kernelReleasePath, bootReleasePath
	t.Cleanup(func() {
		installedDBPath, alpineReleasePath, kernelReleasePath, bootReleasePath = oldDB, oldAlpine, oldKernel, oldBoot
		componentCache.list = nil
	})
	dir := t.TempDir()
	installedDBPath = filepath.Join(dir, "installed")
	alpineReleasePath, kernelReleasePath, bootReleasePath = filepath.Join(dir, "alpine"), filepath.Join(dir, "kernel"), filepath.Join(dir, "boot")
	db := "P:zlib\nV:1.3.2-r0\n\nP:nanokvm-rustdesk\nV:0.5.3-r5\n\nP:nanokvm-app\nV:3.1.0-r0\n\nP:nanokvm\nV:3.1.0-r0\n\nP:nanokvmish\nV:1-r0\n\n"
	for path, data := range map[string]string{installedDBPath: db, alpineReleasePath: "3.24.2\n", kernelReleasePath: "7.2.10-nanokvm-os-r5\n", bootReleasePath: "7.2.11-nanokvm-os-r1\n"} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := saveNotice(UpdateNotice{State: "ok", CheckedAt: 1, Updates: []SoftwareUpdate{{Name: "nanokvm-app", Installed: "3.1.0-r0", Available: "3.1.1-r0"}}}); err != nil {
		t.Fatal(err)
	}
	got := GetComponents()
	want := Components{
		Packages: []Component{{Name: "nanokvm", Installed: "3.1.0-r0"}, {Name: "nanokvm-app", Installed: "3.1.0-r0", Available: "3.1.1-r0"},
			{Name: "nanokvm-rustdesk", Installed: "0.5.3-r5"}},
		Alpine: "3.24.2", Kernel: "7.2.10-nanokvm-os-r5", BootKernel: "7.2.11-nanokvm-os-r1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("components=%+v", got)
	}
	// The cache follows the file.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(installedDBPath, []byte("P:nanokvm\nV:3.1.1-r0\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if p := GetComponents().Packages; len(p) != 1 || p[0].Installed != "3.1.1-r0" {
		t.Fatalf("stale cache: %+v", p)
	}
}
