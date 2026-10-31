package recovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) Engine {
	t.Helper()
	e := Engine{Root: t.TempDir(), Version: "3.4.0", Board: "PCIE"}
	for name, data := range map[string]string{
		"etc/shadow":        "root:$6$test:1:0:99999:7:::\nchrony:!:1:0:99999:7:::\n",
		"etc/apk/world":     "alpine-baselayout\napk-tools\nbusybox\nnanokvm-release=3.4.0-r0\ncurl\nhtop\nnanokvm-rustdesk\n",
		"etc/kvm/web-title": "custom", "etc/kvm/wifi.ssid": "private-ssid", "root/.ssh/authorized_keys": "ssh-ed25519 test-key",
		"etc/nanokvm-rustdesk/config.json": "{}", "root/.picoclaw/config.json": "{}",
		"etc/kvm/acme/data/account.conf": "secret", "etc/kvm/acme/bin/acme.sh": "program",
		"usr/bin/htop": "program", "usr/bin/picoclaw": "program", "data/installation.iso": "user-data",
		"etc/kvm/hw": "PCIE", "etc/kvm/.jwt_secret": "old-token-secret",
		"etc/hostname": "old", "etc/hosts": "127.0.0.1 old", "usr/share/zoneinfo/UTC": "TZif-test",
		"etc/kvm/notify/config.json":    `{"channels":[{"type":"telegram","settings":{"token":"secret"}}]}`,
		"etc/kvm/notify/addresses.json": `{"admin":["192.0.2.1"]}`,
	} {
		if err := write(e.Root, name, &File{Data: []byte(data), Mode: 0600}); err != nil {
			t.Fatal(err)
		}
	}
	if err := write(e.Root, "etc/kvm/cron/crontabs/root", &File{Data: []byte("# nanokvm-apk-index-refresh x\n0 * * * * /usr/sbin/nkos-update refresh-indexes\n* * * * * true\n"), Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("kvm/cron/crontabs", e.Root+"/etc/crontabs"); err != nil {
		t.Fatal(err)
	}
	return e
}
func contents(t *testing.T, e Engine, name string) string {
	t.Helper()
	f, err := read(e.Root, name, MaxFile)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		return ""
	}
	return string(f.Data)
}
func TestArchiveAuthenticationAndPolicy(t *testing.T) {
	a := Archive{Format: 1, Version: "3.4.0", Board: "PCIE", Created: time.Now(), Files: []File{{Path: "etc/kvm/web-title", Data: []byte("secret"), Mode: 0600}}}
	encrypted, err := Seal(a, "long-backup-password")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("secret")) {
		t.Fatal("plaintext leaked")
	}
	got, err := Open(encrypted, "long-backup-password")
	if err != nil || !reflect.DeepEqual(got.Files, a.Files) {
		t.Fatalf("roundtrip: %v", err)
	}
	for _, variant := range []string{"password", "ciphertext", "salt", "nonce"} {
		t.Run(variant, func(t *testing.T) {
			b := bytes.Clone(encrypted)
			pw := "long-backup-password"
			switch variant {
			case "password":
				pw = "wrong-backup-password"
			case "ciphertext":
				b[len(b)-1] ^= 1
			case "salt":
				b[len(magic)] ^= 1
			case "nonce":
				b[len(magic)+16] ^= 1
			}
			if _, err := Open(b, pw); err == nil {
				t.Fatal("unauthenticated archive accepted")
			}
		})
	}
	for _, name := range []string{"../etc/shadow", "/etc/kvm/pwd", "etc/kvm/../shadow", "etc/kvm/usb/../../passwd", "etc/apk/world", "boot/Image", "data/image.iso", "etc/kvm/acme/bin/acme.sh"} {
		b := a
		b.Files = []File{{Path: name, Mode: 0600}}
		if Validate(b) == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	a.Files = append(a.Files, a.Files[0])
	if Validate(a) == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := Seal(a, "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := Open(make([]byte, MaxArchive+1), "long-backup-password"); err == nil {
		t.Fatal("oversized accepted")
	}
}
func TestSnapshotExcludesBinariesAndData(t *testing.T) {
	e := fixture(t)
	a, err := e.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range a.Files {
		if strings.Contains(f.Path, "bin/") || strings.HasPrefix(f.Path, "data/") || f.Path == "etc/kvm/hw" {
			t.Fatalf("unexpected file %s", f.Path)
		}
	}
	saved := map[string]bool{}
	for _, f := range a.Files {
		saved[f.Path] = true
	}
	if !saved["etc/kvm/notify/config.json"] || !saved["etc/kvm/notify/addresses.json"] {
		t.Fatal("notification settings missing from the backup")
	}
	if a.RootHash != "$6$test" || !reflect.DeepEqual(a.Packages, []string{"alpine-baselayout", "apk-tools", "busybox", "nanokvm-release", "curl", "htop", "nanokvm-rustdesk"}) {
		t.Fatalf("inventory %v", a.Packages)
	}
}
func TestResetMatrix(t *testing.T) {
	for _, keepUsers := range []bool{false, true} {
		for _, keepApps := range []bool{false, true} {
			t.Run(strings.Join([]string{boolText(keepUsers), boolText(keepApps)}, "-"), func(t *testing.T) {
				e := fixture(t)
				// A valid web account file is required so reset tests exercise actual users.
				accounts := `{"version":1,"users":[{"username":"admin","password":"hash","role":"admin","enabled":true,"tokenVersion":42,"systemAccount":true}]}`
				if err := write(e.Root, "etc/kvm/pwd", &File{Data: []byte(accounts), Mode: 0600}); err != nil {
					t.Fatal(err)
				}
				p, v, err := e.Prepare(nil, Options{Action: "reset", KeepUsers: keepUsers, KeepApps: keepApps})
				if err != nil {
					t.Fatal(err)
				}
				_, v2, err := e.Prepare(nil, p.Options)
				if err != nil || v.Token != v2.Token {
					t.Fatal("preview unstable")
				}
				if err = e.Stage(p); err != nil {
					t.Fatal(err)
				}
				removed := false
				if err = e.Resume(func(names []string) error {
					removed = true
					if !reflect.DeepEqual(names, []string{"curl", "htop", "nanokvm-rustdesk"}) {
						t.Fatalf("unsafe removals %v", names)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if removed == keepApps {
					t.Fatal("keep apps did not protect all APK packages")
				}
				if got := contents(t, e, "etc/nanokvm-rustdesk/config.json"); got != "" {
					t.Fatal("app settings retained")
				}
				if got := contents(t, e, "etc/kvm/acme/data/account.conf"); got != "" {
					t.Fatal("ACME settings retained")
				}
				if contents(t, e, "etc/kvm/notify/config.json") != "" || contents(t, e, "etc/kvm/notify/addresses.json") != "" {
					t.Fatal("notification channels or login addresses retained")
				}
				if contents(t, e, "etc/kvm/acme/bin/acme.sh") != "program" {
					t.Fatal("engine touched program files")
				}
				if contents(t, e, "data/installation.iso") != "user-data" || contents(t, e, "etc/kvm/hw") != "PCIE" {
					t.Fatal("data/hardware affected")
				}
				if keepUsers {
					if contents(t, e, "etc/kvm/pwd") != accounts || contents(t, e, "root/.ssh/authorized_keys") == "" || !strings.Contains(contents(t, e, "etc/shadow"), "root:$6$test:") {
						t.Fatal("users lost")
					}
				} else {
					if contents(t, e, "etc/kvm/pwd") != "" || contents(t, e, "root/.ssh/authorized_keys") != "" || !strings.Contains(contents(t, e, "etc/shadow"), "root:!:") {
						t.Fatal("users retained")
					}
					if f, _ := read(e.Root, "etc/kvm/ssh_stop", 100); f == nil {
						t.Fatal("SSH not disabled")
					}
				}
				if contents(t, e, "etc/kvm/usb/.layout-v1") == "" {
					t.Fatal("USB migration marker lost")
				}
				if e.Pending() || e.Status().State != "complete" {
					t.Fatal("journal not cleared")
				}
			})
		}
	}
}
func boolText(v bool) string {
	if v {
		return "keep"
	}
	return "reset"
}
func TestMigrationAndCompatibility(t *testing.T) {
	source := fixture(t)
	a, err := source.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	target := fixture(t)
	target.Board = "CUBE"
	if err = write(target.Root, "etc/kvm/wifi.ssid", &File{Data: []byte("destination-network"), Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	p, v, err := target.Prepare(&a, Options{Action: "restore", Mode: "migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Skipped) == 0 {
		t.Fatal("no migration exclusions")
	}
	for _, c := range p.Changes {
		if c.Path == "etc/shadow" || c.Path == "etc/kvm/wifi.ssid" || c.Path == "root/.ssh/authorized_keys" ||
			strings.HasPrefix(c.Path, "etc/kvm/notify/") {
			t.Fatalf("migration clones identity: %s", c.Path)
		}
	}
	if _, _, err = target.Prepare(&a, Options{Action: "restore", Mode: "device"}); err == nil {
		t.Fatal("cross-board full restore allowed")
	}
	a.Version = "3.5.0"
	if _, _, err = target.Prepare(&a, Options{Action: "restore", Mode: "migrate"}); err == nil {
		t.Fatal("newer archive accepted")
	}
	a.Version = "4.0.0"
	if _, _, err = target.Prepare(&a, Options{Action: "restore", Mode: "migrate"}); err == nil {
		t.Fatal("major mismatch accepted")
	}
}
func TestSymlinksSpecialFilesAndLimits(t *testing.T) {
	for _, kind := range []string{"leaf", "parent"} {
		t.Run(kind, func(t *testing.T) {
			e := fixture(t)
			outside := t.TempDir()
			target := filepath.Join(outside, "secret")
			os.WriteFile(target, []byte("untouched"), 0600)
			if kind == "leaf" {
				os.Remove(e.Root + "/etc/kvm/web-title")
				os.Symlink(target, e.Root+"/etc/kvm/web-title")
			} else {
				os.MkdirAll(e.Root+"/etc/kvm", 0700)
				os.Symlink(outside, e.Root+"/etc/kvm/scripts")
			}
			if _, err := e.Snapshot(); err == nil {
				t.Fatal("symlink accepted")
			}
			b, _ := os.ReadFile(target)
			if string(b) != "untouched" {
				t.Fatal("outside file touched")
			}
		})
	}
	e := fixture(t)
	os.WriteFile(e.Root+"/etc/kvm/web-title", make([]byte, MaxFile+1), 0600)
	if _, err := e.Snapshot(); err == nil {
		t.Fatal("oversize settings accepted")
	}
}
func TestCrashReplayAndRollback(t *testing.T) {
	e := fixture(t)
	p, _, err := e.Prepare(nil, Options{Action: "reset", KeepUsers: true, KeepApps: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Stage(p); err != nil {
		t.Fatal(err)
	}
	// Emulate power loss after the started journal and first replacements.
	p.Started = true
	journal, _ := json.Marshal(p)
	if err = write(e.Root, PendingPath, &File{Data: journal, Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Changes[:3] {
		if err = write(e.Root, c.Path, c.After); err != nil {
			t.Fatal(err)
		}
	}
	if err = e.Resume(func([]string) error { t.Fatal("unexpected APK operation"); return nil }); err != nil {
		t.Fatal(err)
	}
	if contents(t, e, "etc/kvm/web-title") != "" || e.Pending() {
		t.Fatal("interrupted operation not completed")
	}
	e = fixture(t)
	p, _, err = e.Prepare(nil, Options{Action: "reset", KeepUsers: true, KeepApps: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Stage(p); err != nil {
		t.Fatal(err)
	}
	calls := 0
	e.writeFile = func(name string, f *File) error {
		calls++
		if calls == 3 {
			return errors.New("injected disk error")
		}
		return write(e.Root, name, f)
	}
	if err = e.Resume(func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if contents(t, e, "etc/kvm/web-title") != "custom" || contents(t, e, "etc/kvm/acme/data/account.conf") != "secret" || e.Status().State != "failed" {
		t.Fatal("rollback failed")
	}
}
func TestStalePreviewAndJournalValidation(t *testing.T) {
	e := fixture(t)
	p, _, err := e.Prepare(nil, Options{Action: "reset", KeepUsers: true, KeepApps: true})
	if err != nil {
		t.Fatal(err)
	}
	write(e.Root, "etc/kvm/web-title", &File{Data: []byte("changed"), Mode: 0600})
	if e.Stage(p) == nil {
		t.Fatal("stale preview applied")
	}
	p.Changes[0].Path = "etc/passwd"
	if validatePlan(p) == nil {
		t.Fatal("unsafe journal accepted")
	}
	for _, input := range []string{`{"action":"reset"}`, `{"action":"reset","keepUsers":true,"keepApps":null}`} {
		var o Options
		if json.Unmarshal([]byte(input), &o) == nil {
			t.Fatal("implicit destructive option")
		}
	}
	if _, err = removable([]byte("busybox\nhtop\n")); err == nil {
		t.Fatal("missing OS metapackage accepted")
	}
}

func TestInterruptedPackageRemovalAndChangedWorld(t *testing.T) {
	original := []byte("nanokvm-release=3.4.0-r0\nhtop\ncurl\n")
	names, err := remainingRemovals(original, []byte("nanokvm-release=3.4.0-r0\ncurl\n"), []string{"curl", "htop"})
	if err != nil || !reflect.DeepEqual(names, []string{"curl"}) {
		t.Fatalf("retry %v %v", names, err)
	}
	if _, err = remainingRemovals(original, []byte("nanokvm-release=3.4.0-r0\nnew-package\n"), []string{"curl", "htop"}); err == nil {
		t.Fatal("changed world accepted")
	}
}
func TestACMECodeRemovalIsBounded(t *testing.T) {
	e := fixture(t)
	if err := removeACMEBinaries(e.Root); err != nil {
		t.Fatal(err)
	}
	if contents(t, e, "etc/kvm/acme/data/account.conf") != "secret" {
		t.Fatal("configuration deleted by code removal")
	}
	e = fixture(t)
	outside := t.TempDir()
	os.WriteFile(outside+"/file", []byte("outside"), 0600)
	os.Symlink(outside, e.Root+"/etc/kvm/acme/bin/link")
	if removeACMEBinaries(e.Root) == nil {
		t.Fatal("symlink accepted")
	}
	if b, _ := os.ReadFile(outside + "/file"); string(b) != "outside" {
		t.Fatal("outside deleted")
	}
}

func TestEverySettingsFailureRollsBack(t *testing.T) {
	probe := fixture(t)
	plan, _, err := probe.Prepare(nil, Options{Action: "reset", KeepUsers: false, KeepApps: true})
	if err != nil {
		t.Fatal(err)
	}
	for boundary := 1; boundary <= len(plan.Changes); boundary++ {
		e := fixture(t)
		before, err := inventory(e.Root)
		if err != nil {
			t.Fatal(err)
		}
		p, _, err := e.Prepare(nil, plan.Options)
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Stage(p); err != nil {
			t.Fatal(err)
		}
		n := 0
		e.writeFile = func(name string, f *File) error {
			n++
			if n == boundary {
				return errors.New("injected write failure")
			}
			return write(e.Root, name, f)
		}
		if err = e.Resume(func([]string) error { return nil }); err != nil {
			t.Fatalf("boundary %d: %v", boundary, err)
		}
		after, err := inventory(e.Root)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("boundary %d lost settings: %v", boundary, err)
		}
	}
}
func TestCorruptJournalDoesNotTouchSettings(t *testing.T) {
	e := fixture(t)
	write(e.Root, PendingPath, &File{Data: []byte(`{"started":true,"options":{"action":"reset","keepUsers":true,"keepApps":true},"changes":[{"path":"etc/passwd"}]}`), Mode: 0600})
	if e.Resume(func([]string) error { t.Fatal("package operation on invalid journal"); return nil }) == nil {
		t.Fatal("corrupt journal accepted")
	}
	if contents(t, e, "etc/kvm/web-title") != "custom" || !e.Pending() {
		t.Fatal("failed-closed invariant lost")
	}
}
func TestStandardTimezoneLinkAndParentAlias(t *testing.T) {
	e := fixture(t)
	os.Symlink("/usr/share/zoneinfo/UTC", e.Root+"/etc/localtime")
	if _, err := e.Snapshot(); err != nil {
		t.Fatal(err)
	}
	p, _, err := e.Prepare(nil, Options{Action: "reset", KeepUsers: true, KeepApps: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Stage(p); err != nil {
		t.Fatal(err)
	}
	if err = e.Resume(func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	e = fixture(t)
	outside := t.TempDir()
	os.Mkdir(outside+"/bin", 0700)
	os.WriteFile(outside+"/bin/secret", []byte("keep"), 0600)
	os.RemoveAll(e.Root + "/etc/kvm/acme")
	os.Symlink(outside, e.Root+"/etc/kvm/acme")
	if removeACMEBinaries(e.Root) == nil {
		t.Fatal("parent alias accepted")
	}
	if b, _ := os.ReadFile(outside + "/bin/secret"); string(b) != "keep" {
		t.Fatal("outside tree changed")
	}
}

func TestFATWriteIntegration(t *testing.T) {
	root := os.Getenv("NKOS_RECOVERY_FAT_ROOT")
	if root == "" {
		t.Skip("requires disposable FAT mount")
	}
	name := "boot/wifi.ssid"
	if err := write(root, name, &File{Path: name, Data: []byte("fat-network"), Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	f, err := read(root, name, 100)
	if err != nil || f == nil || string(f.Data) != "fat-network" {
		t.Fatalf("FAT read %v", err)
	}
	if err = write(root, name, nil); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownFlushIsResetAndRollbackKeepsFinalState(t *testing.T) {
	e := fixture(t)
	p, _, err := e.Prepare(nil, Options{Action: "reset", KeepUsers: true, KeepApps: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Stage(p); err != nil {
		t.Fatal(err)
	}
	write(e.Root, "etc/nanokvm-rustdesk/late.json", &File{Data: []byte("shutdown-settings"), Mode: 0600})
	if err = e.Resume(func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if contents(t, e, "etc/nanokvm-rustdesk/late.json") != "" {
		t.Fatal("shutdown settings survived reset")
	}
}

func TestPreflightFailuresAllowNormalBoot(t *testing.T) {
	for _, kind := range []string{"world-missing", "world-changed", "bad-plan", "inventory-alias"} {
		t.Run(kind, func(t *testing.T) {
			e := fixture(t)
			p, _, err := e.Prepare(nil, Options{Action: "reset", KeepUsers: true, KeepApps: false})
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Stage(p); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "world-missing":
				os.Remove(e.Root + "/etc/apk/world")
			case "world-changed":
				write(e.Root, "etc/apk/world", &File{Data: []byte("nanokvm-release\nnew-package\n"), Mode: 0600})
			case "bad-plan":
				p.Changes[0].Path = "etc/passwd"
				data, _ := json.Marshal(p)
				write(e.Root, PendingPath, &File{Data: data, Mode: 0600})
			case "inventory-alias":
				os.Symlink("/tmp", e.Root+"/etc/kvm/network")
			}
			if err = e.Resume(func([]string) error { t.Fatal("preflight removed packages"); return nil }); err != nil {
				t.Fatal(err)
			}
			if e.Pending() || e.Status().State != "failed" || contents(t, e, "etc/kvm/web-title") != "custom" {
				t.Fatal("preflight did not safely cancel")
			}
			if _, err = os.Stat(e.Root + "/var/lib/nanokvm-recovery/failed.json"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestInterruptedRollbackDoesNotReplayReset(t *testing.T) {
	e := fixture(t)
	before, err := inventory(e.Root)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := e.Prepare(nil, Options{Action: "reset", KeepUsers: false, KeepApps: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Stage(p); err != nil {
		t.Fatal(err)
	}
	count := 0
	e.writeFile = func(name string, f *File) error {
		count++
		if count == 3 || count == 4 {
			return errors.New("disk failure")
		}
		return write(e.Root, name, f)
	}
	if e.Resume(func([]string) error { return nil }) == nil || !e.Pending() {
		t.Fatal("partial rollback must remain pending")
	}
	f, err := read(e.Root, PendingPath, 2*MaxArchive)
	if err != nil {
		t.Fatal(err)
	}
	var pending Plan
	if err = json.Unmarshal(f.Data, &pending); err != nil || !pending.RollingBack {
		t.Fatal("rollback phase not durable", err)
	}
	e.writeFile = nil
	if err = e.Resume(func([]string) error { t.Fatal("rollback removed packages"); return nil }); err != nil {
		t.Fatal(err)
	}
	after, err := inventory(e.Root)
	if err != nil || !reflect.DeepEqual(before, after) || e.Pending() || e.Status().State != "failed" {
		t.Fatal("rollback was not resumed", err)
	}
}
func TestOptionalNanokvmPackagesAreRemovable(t *testing.T) {
	names, err := removable([]byte("nanokvm-release\nnanokvm-base\nnanokvm-ipmi\nnanokvm-future-addon\n"))
	if err != nil || !reflect.DeepEqual(names, []string{"nanokvm-future-addon", "nanokvm-ipmi"}) {
		t.Fatal(names, err)
	}
	if portable("etc/kvm/bmc/config.json") {
		t.Fatal("BMC credentials portable")
	}
}

// Saved Wi-Fi networks are restored on the device itself, never on a clone.
func TestSavedWifiNetworksAreDeviceSettings(t *testing.T) {
	for _, p := range []string{"etc/kvm/wifi.saved/4f6666696365/pass", "etc/kvm/wifi.saved/4f6666696365/auto"} {
		if !allowed(p) || portable(p) {
			t.Fatalf("%s: allowed %v, portable %v", p, allowed(p), portable(p))
		}
	}
	if allowed("etc/kvm/wifi.saved") || allowed("etc/kvm/wifi.savedx/a") {
		t.Fatal("paths outside the saved networks allowed")
	}
}
func TestMalformedJournalRetainsRepairEvidence(t *testing.T) {
	e := fixture(t)
	write(e.Root, PendingPath, &File{Data: []byte("{truncated"), Mode: 0600})
	if e.Resume(func([]string) error { t.Fatal("unexpected removal"); return nil }) == nil || !e.Pending() {
		t.Fatal("unknown partial state discarded")
	}
}
