package recovery

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The ZeroTier identity and its networks are backed up, restored on the same
// device and never cloned: two devices with one identity break each other.
func TestZerotierStatePolicy(t *testing.T) {
	for _, p := range []string{
		"var/lib/zerotier-one/identity.secret", "var/lib/zerotier-one/identity.public",
		"var/lib/zerotier-one/local.conf", "var/lib/zerotier-one/networks.d/8056c2e21c000001.conf",
		"var/lib/zerotier-one/networks.d/8056c2e21c000001.local.conf",
	} {
		if !allowed(p) {
			t.Errorf("%s not in the backup", p)
		}
		if portable(p) {
			t.Errorf("%s copied to another device", p)
		}
	}
	// The API token, caches and the planet file are not settings.
	for _, p := range []string{
		"var/lib/zerotier-one/authtoken.secret", "var/lib/zerotier-one/peers.d/1234567890.peer",
		"var/lib/zerotier-one/planet", "var/lib/zerotier-one/zerotier-one.pid",
	} {
		if allowed(p) {
			t.Errorf("%s in the backup", p)
		}
	}
}

// A backup made before ZeroTier was backed up says nothing about it: restoring
// one keeps the device's identity and networks, in the plan and at boot, as
// does a restore journal queued by an earlier release. A backup that covers
// ZeroTier restores its recorded state, even an empty one.
func TestZerotierStateSurvivesOlderBackups(t *testing.T) {
	identity := "var/lib/zerotier-one/identity.secret"
	legacy := Archive{Format: 1, Version: "3.4.0", Board: "PCIE", Created: time.Now(),
		Files: []File{{Path: "etc/kvm/web-title", Data: []byte("restored"), Mode: 0600}}}
	for _, covered := range []bool{false, true} {
		e := fixture(t)
		write(e.Root, identity, &File{Data: []byte("node-secret"), Mode: 0600})
		a := legacy
		if covered {
			a.Covers = allGroups()
		}
		plan, _, err := e.Prepare(&a, Options{Action: "restore", Mode: "device"})
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Stage(plan); err != nil {
			t.Fatal(err)
		}
		// Without coverage the journal has the shape of one queued by a
		// release before groups existed: no covers field.
		if journal := contents(t, e, PendingPath); strings.Contains(journal, `"covers"`) == !covered {
			t.Fatalf("covered=%v: journal %s", covered, journal[:min(len(journal), 200)])
		}
		// Written after the preview: the boot pass takes inventory again.
		network := "var/lib/zerotier-one/networks.d/8056c2e21c000001.conf"
		write(e.Root, network, &File{Data: []byte("network"), Mode: 0600})
		if err = e.Resume(func([]string) error { return nil }); err != nil {
			t.Fatal(err)
		}
		kept := contents(t, e, identity) == "node-secret" && contents(t, e, network) == "network"
		gone := contents(t, e, identity) == "" && contents(t, e, network) == ""
		if contents(t, e, "etc/kvm/web-title") != "restored" || (!covered && !kept) || (covered && !gone) {
			t.Fatalf("covered=%v: identity %q, network %q", covered, contents(t, e, identity), contents(t, e, network))
		}
	}
	// A backup covers ZeroTier only once the device has its state.
	e := fixture(t)
	if a, err := e.Snapshot(); err != nil || len(a.Covers) != 0 {
		t.Fatalf("snapshot without ZeroTier covers %v, %v", a.Covers, err)
	}
	write(e.Root, identity, &File{Data: []byte("node-secret"), Mode: 0600})
	if a, err := e.Snapshot(); err != nil || !slices.Equal(a.Covers, allGroups()) {
		t.Fatalf("snapshot with ZeroTier covers %v, %v", a.Covers, err)
	}
	// ZeroTier files only in an archive that covers them.
	a := legacy
	a.Files = append(a.Files, File{Path: identity, Data: []byte("x"), Mode: 0600})
	if Validate(a) == nil {
		t.Fatal("ZeroTier state accepted from an archive that does not cover it")
	}
	// A group of a later release asks for that release.
	a = legacy
	a.Covers = []string{"zerotier", "later"}
	if err := Validate(a); err == nil || !strings.Contains(err.Error(), "later application version") {
		t.Fatalf("unknown group: %v", err)
	}
	if p := (Plan{Options: Options{Action: "restore", Mode: "device"},
		Changes: []Change{{Path: identity}}}); validatePlan(p) == nil || !strings.Contains(validatePlan(p).Error(), "groups") {
		t.Fatal("journal change outside its groups accepted")
	}
}

// A factory reset requested now deletes the ZeroTier state, also state written
// after the preview. One queued by a release before groups existed, which
// kept ZeroTier's files, still keeps them when the boot pass takes inventory.
func TestZerotierStateSurvivesQueuedResets(t *testing.T) {
	identity := "var/lib/zerotier-one/identity.secret"
	for _, legacy := range []bool{false, true} {
		e := fixture(t)
		write(e.Root, "etc/kvm/web-title", &File{Data: []byte("custom"), Mode: 0600})
		plan, _, err := e.Prepare(nil, Options{Action: "reset", KeepUsers: true, KeepApps: true})
		if err != nil {
			t.Fatal(err)
		}
		if legacy {
			plan.Covers = nil
		}
		if err = e.Stage(plan); err != nil {
			t.Fatal(err)
		}
		if journal := contents(t, e, PendingPath); strings.Contains(journal, `"covers"`) == legacy {
			t.Fatalf("legacy=%v: journal %s", legacy, journal[:min(len(journal), 200)])
		}
		write(e.Root, identity, &File{Data: []byte("node-secret"), Mode: 0600})
		if err = e.Resume(func([]string) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if got := contents(t, e, identity); contents(t, e, "etc/kvm/web-title") != "" || (got == "node-secret") != legacy {
			t.Fatalf("legacy=%v: identity %q, web title %q", legacy, got, contents(t, e, "etc/kvm/web-title"))
		}
	}
}

// A backup made before ZeroTier first started, with only its configuration
// and a preseeded network, restores the configuration and keeps the identity
// and networks made since.
func TestZerotierPreStartBackupKeepsLaterIdentity(t *testing.T) {
	dir := "var/lib/zerotier-one/"
	e := fixture(t)
	write(e.Root, dir+"local.conf", &File{Data: []byte("saved"), Mode: 0600})
	write(e.Root, dir+"networks.d/8056c2e21c000001.conf", &File{Data: []byte("preseeded"), Mode: 0600})
	a, err := e.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Covers, []string{"zerotier-config"}) ||
		slices.ContainsFunc(a.Files, func(f File) bool { return group(f.Path) == "zerotier" }) {
		t.Fatalf("pre-start backup covers %v", a.Covers)
	}
	// The first start.
	write(e.Root, dir+"identity.secret", &File{Data: []byte("node-secret"), Mode: 0600})
	write(e.Root, dir+"networks.d/8056c2e21c000002.conf", &File{Data: []byte("joined"), Mode: 0600})
	write(e.Root, dir+"local.conf", &File{Data: []byte("changed"), Mode: 0600})
	plan, _, err := e.Prepare(&a, Options{Action: "restore", Mode: "device"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Stage(plan); err != nil {
		t.Fatal(err)
	}
	if err = e.Resume(func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"identity.secret": "node-secret", "local.conf": "saved",
		"networks.d/8056c2e21c000001.conf": "preseeded", "networks.d/8056c2e21c000002.conf": "joined"} {
		if got := contents(t, e, dir+name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	// Once ZeroTier started, a backup without local.conf records that there
	// was none.
	e = fixture(t)
	write(e.Root, dir+"identity.secret", &File{Data: []byte("node-secret"), Mode: 0600})
	if a, err = e.Snapshot(); err != nil {
		t.Fatal(err)
	}
	write(e.Root, dir+"local.conf", &File{Data: []byte("later"), Mode: 0600})
	if plan, _, err = e.Prepare(&a, Options{Action: "restore", Mode: "device"}); err != nil {
		t.Fatal(err)
	}
	if err = e.Stage(plan); err != nil {
		t.Fatal(err)
	}
	if err = e.Resume(func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if contents(t, e, dir+"local.conf") != "" || contents(t, e, dir+"identity.secret") != "node-secret" {
		t.Fatalf("local.conf %q, identity %q", contents(t, e, dir+"local.conf"), contents(t, e, dir+"identity.secret"))
	}
}
