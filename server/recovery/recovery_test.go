package recovery

import (
	"bytes"

	"os"
	"path/filepath"
	"reflect"

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

// A valid web account file is required so reset tests exercise actual users.

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

// Emulate power loss after the started journal and first replacements.

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

// Saved Wi-Fi networks are restored on the device itself, never on a clone.

func TestMalformedJournalRetainsRepairEvidence(t *testing.T) {
	e := fixture(t)
	write(e.Root, PendingPath, &File{Data: []byte("{truncated"), Mode: 0600})
	if e.Resume(func([]string) error { t.Fatal("unexpected removal"); return nil }) == nil || !e.Pending() {
		t.Fatal("unknown partial state discarded")
	}
}
