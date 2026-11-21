package osupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageSelectionBoundsAndNamespace(t *testing.T) {
	old := worldPath
	worldPath = filepath.Join(t.TempDir(), "world")
	t.Cleanup(func() { worldPath = old })
	world := "nanokvm><Q1a=\nnanokvm-rustdesk@testing><Q1b=\nnanokvmish><Q1c=\ntailscale><Q1d=\n" +
		"exact=1.0-r0\nupper<2\nfuzzy~1.0\ntagged@testing<=2\nlower>=1.0\nnormal\n"
	if err := os.WriteFile(worldPath, []byte(world), 0600); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(nanokvmFilePins(), ","); got != "nanokvm,nanokvm-rustdesk@testing" {
		t.Fatalf("file selections: %s", got)
	}
	skip, bounded := upgradeSelections()
	if got := strings.Join(skip, ","); got != "nanokvmish,tailscale,exact,upper,fuzzy,tagged" {
		t.Fatalf("skipped: %s", got)
	}
	if got := strings.Join(bounded, ","); got != "exact,upper,fuzzy,tagged" {
		t.Fatalf("bounded: %s", got)
	}
	plan := "(1/5) Upgrading upper (1.0-r0 -> 1.1-r0)\n(2/5) Upgrading fuzzy (1.0-r0 -> 1.0-r1)\n" +
		"(3/5) Upgrading normal (1.0-r0 -> 2.0-r0)\n(4/5) Installing dependency (1.0-r0)\n(5/5) Purging removed (1.0-r0)\n"
	if got := boundedUpgrades(plan, bounded); got != "upper-1.0-r0 < 1.1-r0\nfuzzy-1.0-r0 < 1.0-r1" {
		t.Fatalf("bounded upgrades: %s", got)
	}
}

func TestPendingUpgradesRefreshesInstalledVersion(t *testing.T) {
	listing := "fixture-1.0-r0 < 2.0-r0\nremoved-1.0-r0 < 2.0-r0\nalready-1.0-r0 < 2.0-r0"
	got := pendingUpgrades(listing, map[string]string{"fixture": "1.1-r0", "already": "2.0-r0"})
	if got != "fixture-1.1-r0 < 2.0-r0" {
		t.Fatalf("pending: %s", got)
	}
}

// A failed extraction or install script leaves the package recorded with
// "f" or "s" on its f: line; a retry must see it as broken, not installed.
// So must a broken package reached through the dependencies, by name or by a
// provided name, while a broken package nothing named depends on, or one only
// named as a conflict, is left out.
func TestPackageStateReportsRecordedButBroken(t *testing.T) {
	db := "C:Q1a=\nP:script\nV:1.0-r0\nf:s\n\n" +
		"C:Q1b=\nP:files\nV:1.0-r0\nf:fS\n\n" +
		"C:Q1c=\nP:complete\nV:1.0-r0\nD:!unrelated so:libdep.so.1 /bin/sh\nf:S\n\n" +
		"C:Q1d=\nP:plain\nV:1.0-r0\n\n" +
		"C:Q1e=\nP:libdep\nV:1.0-r0\nD:inner>=1\np:so:libdep.so.1=1.0.0\n\n" +
		"C:Q1f=\nP:inner\nV:1.0-r0\nf:s\n\n" +
		"C:Q1g=\nP:shell\nV:1.0-r0\np:/bin/sh\n\n" +
		"C:Q1h=\nP:unrelated\nV:1.0-r0\nf:s\n"
	if err := os.WriteFile(installedDBPath, []byte(db), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(installedDBPath) })
	missing, broken, err := PackageState("script", "files", "complete", "plain", "absent")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(missing, ","); got != "absent" {
		t.Fatalf("missing: %s", got)
	}
	if got := strings.Join(broken, ","); got != "script,files,inner" {
		t.Fatalf("broken: %s", got)
	}
}
