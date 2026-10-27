package osupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestChannelSwitchPreservesRepositoriesAndClearsOffers(t *testing.T) {
	dir := communityFixture(t, "# keep this\n"+communityRepositories+"@custom https://example.test/repo\n")
	old := apkStateDir
	apkStateDir = filepath.Join(dir, "state")
	t.Cleanup(func() { apkStateDir = old })
	if err := os.MkdirAll(apkStateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := saveNotice(UpdateNotice{State: "ok", CheckedAt: 42, Updates: []SoftwareUpdate{{Name: "nanokvm", Available: "9.0"}}}); err != nil {
		t.Fatal(err)
	}
	if err := saveAPKStatus(APKStatus{State: "ready", Upgrades: "old beta"}); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(repositoriesPath)
	if err := SetChannel("beta"); err != nil {
		t.Fatal(err)
	}
	if got := GetChannel(); got.Channel != "beta" || !got.Available {
		t.Fatal(got)
	}
	got, _ := os.ReadFile(repositoriesPath)
	want := strings.Replace(string(original), "NanoKVM-OS/alpine/", "NanoKVM-OS/beta/alpine/", 1)
	if string(got) != want {
		t.Fatalf("repositories changed: %s", got)
	}
	if n := GetNotice(); n.CheckedAt != 0 || len(n.Updates) != 0 {
		t.Fatal(n)
	}
	if s := GetAPKStatus(); s.State != "idle" || s.Upgrades != "" {
		t.Fatal(s)
	}
	if err := SetChannel("stable"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(repositoriesPath)
	if string(got) != string(original) {
		t.Fatalf("roundtrip changed unrelated lines: %s", got)
	}
}

func TestChannelRejectsCustomAndAmbiguousRepositories(t *testing.T) {
	for _, data := range []string{"https://example.test/nanokvm/repo\n", communityOff + communityOff,
		communityOff + "https://example.test/nanokvm/repo\n", "@testing " + communityOff} {
		t.Run(data, func(t *testing.T) {
			communityFixture(t, data)
			old := apkStateDir
			apkStateDir = t.TempDir()
			t.Cleanup(func() { apkStateDir = old })
			if got := GetChannel(); got.Available || got.Channel != "custom" {
				t.Fatal("custom repository accepted")
			}
			if SetChannel("beta") == nil {
				t.Fatal("changed custom repository")
			}
			got, _ := os.ReadFile(repositoriesPath)
			if string(got) != data {
				t.Fatal("modified")
			}
		})
	}
}

func TestChannelSerializesWithUpdatesAndDatabase(t *testing.T) {
	communityFixture(t, communityOff)
	old := apkStateDir
	apkStateDir = t.TempDir()
	t.Cleanup(func() { apkStateDir = old })
	updateLock = func() (*os.File, error) { return nil, errors.New("busy") }
	if SetChannel("beta") == nil {
		t.Fatal("ignored active update")
	}
	if SetChannel("invalid") == nil {
		t.Fatal("invalid channel")
	}
	updateLock = func() (*os.File, error) { return os.Open(apkStateDir) }
	db, err := lockCommunityDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if SetChannel("beta") == nil {
		t.Fatal("ignored terminal APK lock")
	}
	syscall.Flock(int(db.Fd()), syscall.LOCK_UN)
	// Verify the real notice lock excludes another reader while switching.
	lock, err := lockNotice(context.Background(), false)
	if err != nil || lock == nil {
		t.Fatal(err)
	}
	second, err := lockNotice(context.Background(), false)
	if err != nil || second != nil {
		t.Fatal("notice lock not exclusive")
	}
	lock.Close()
	if err := SetChannel("beta"); err != nil {
		t.Fatal(err)
	}
}

// A delayed old-channel result must never survive a completed switch.
func TestChannelCannotRaceRunningNoticeCheck(t *testing.T) {
	communityFixture(t, communityOff)
	noticeFixture(t, communityOff)
	// The notice offers only packages still installed below the update.
	if err := os.WriteFile(installedDBPath, []byte("P:nanokvm\nV:3.3.3-r0\n\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	noticeApk = func(_ context.Context, args ...string) (string, error) {
		if args[len(args)-1] == "update" {
			close(entered)
			<-release
			return "OK", nil
		}
		return "nanokvm-3.3.3-r0 < 3.3.4-r0\n", nil
	}
	go func() { done <- CheckUpdates(context.Background(), true) }()
	<-entered
	err := SetChannel("beta")
	got := GetChannel()
	close(release)
	checkErr := <-done
	if err == nil || got.Channel != "stable" || checkErr != nil {
		t.Fatalf("switch=%v channel=%+v check=%v", err, got, checkErr)
	}
	if len(GetNotice().Updates) != 1 {
		t.Fatal("fixture did not produce an offer")
	}
	if err := SetChannel("beta"); err != nil {
		t.Fatal(err)
	}
	if n := GetNotice(); len(n.Updates) != 0 || n.CheckedAt != 0 {
		t.Fatal(n)
	}
}

func TestStableRollbackScopeAndPrereleaseDetection(t *testing.T) {
	dir := communityFixture(t, communityOff)
	oldDB, oldWorld := installedDBPath, worldPath
	installedDBPath, worldPath = filepath.Join(dir, "installed"), filepath.Join(dir, "world")
	t.Cleanup(func() { installedDBPath, worldPath = oldDB, oldWorld })
	os.WriteFile(installedDBPath, []byte("P:nanokvm-rustdesk\nV:0.6.0_beta1-r0\n\n"), 0600)
	if GetChannel().RollbackAvailable {
		t.Fatal("add-on exposed firmware rollback")
	}
	os.WriteFile(installedDBPath, []byte("P:nanokvm\nV:3.4.0_beta1-r0\n\n"), 0600)
	if !GetChannel().RollbackAvailable {
		t.Fatal("missing rollback")
	}
	os.WriteFile(repositoriesPath, []byte(strings.Replace(communityOff, "OS/alpine", "OS/beta/alpine", 1)), 0600)
	if GetChannel().RollbackAvailable {
		t.Fatal("rollback while on beta")
	}
	var output []string
	for _, name := range corePackages {
		output = append(output, name+"-3.3.4-r0")
	}
	good := strings.Join(output, "\n")
	if _, err := stablePackageTargets(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Join(output[1:], "\n"), strings.Replace(good, "3.3.4", "3.4.0_beta1", 1)} {
		if _, err := stablePackageTargets(bad); err == nil {
			t.Fatal("unsafe rollback set accepted")
		}
	}
}

func TestStableRollbackBeforeCustomOpenSSL(t *testing.T) {
	var output []string
	for _, name := range corePackages {
		if name != "nanokvm-openssl" {
			output = append(output, name+"-3.5.0-r0")
		}
	}
	targets, err := stablePackageTargets(strings.Join(output, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	joined := " " + strings.Join(targets, " ") + " "
	if !strings.Contains(joined, " !nanokvm-openssl ") || !strings.Contains(joined, " openssl ") {
		t.Fatalf("legacy release must atomically restore Alpine OpenSSL: %v", targets)
	}
	if _, err = stablePackageTargets(strings.Join(output[1:], "\n")); err == nil {
		t.Fatal("another missing core component must still reject rollback")
	}
}
