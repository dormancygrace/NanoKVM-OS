package sshkeys

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

func public(t *testing.T) string {
	t.Helper()
	p, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	k, e := ssh.NewPublicKey(p)
	if e != nil {
		t.Fatal(e)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}
func TestImportPreservesExistingRestrictionsAndDeduplicates(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".ssh")
	os.Mkdir(dir, 0755)
	a, b := public(t), public(t)
	original := "# Keep this comment\nfrom=\"192.0.2.1\",no-pty " + a + " restricted"
	path := filepath.Join(dir, "authorized_keys")
	os.WriteFile(path, []byte(original), 0644)
	got, e := Import(home, "# new keys\r\n"+a+" different comment\r\n"+b+" laptop\r\n"+b+" duplicate\n")
	if e != nil || got.Added != 1 || got.Skipped != 2 || len(got.Fingerprints) != 1 {
		t.Fatalf("%+v %v", got, e)
	}
	content, _ := os.ReadFile(path)
	want := original + "\n" + b + " laptop\n"
	if string(content) != want {
		t.Fatalf("changed existing access: %q", content)
	}
	for path, mode := range map[string]os.FileMode{dir: 0700, path: 0600} {
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v %v", path, info, e)
		}
	}
	got, e = Import(home, b)
	if e != nil || got.Added != 0 || got.Skipped != 1 {
		t.Fatalf("%+v %v", got, e)
	}
	content, _ = os.ReadFile(path)
	if string(content) != want {
		t.Fatal("duplicate changed file")
	}
}
func TestRejectEntireInvalidImport(t *testing.T) {
	key := public(t)
	for _, input := range []string{"", "# comment", "garbage", key + "\ninvalid", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc", "command=\"id\" " + key, key + "\x00", key + "\x1b", string([]byte{0xff}), strings.Repeat("a", MaxInput+1), strings.Repeat(key+"\n", 101)} {
		home := t.TempDir()
		_, e := Import(home, input)
		if !errors.Is(e, ErrInvalid) {
			t.Fatalf("expected invalid, got %v", e)
		}
		if _, e = os.Stat(filepath.Join(home, ".ssh")); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("invalid input touched filesystem")
		}
	}
}
func TestAlgorithms(t *testing.T) {
	for _, bits := range []int{1024, 2048} {
		k, e := rsa.GenerateKey(rand.Reader, bits)
		if e != nil {
			t.Fatal(e)
		}
		pub, _ := ssh.NewPublicKey(&k.PublicKey)
		_, e = parse(string(ssh.MarshalAuthorizedKey(pub)))
		if (e == nil) != (bits >= 2048) {
			t.Fatalf("RSA%d: %v", bits, e)
		}
	}
	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		k, _ := ecdsa.GenerateKey(curve, rand.Reader)
		pub, _ := ssh.NewPublicKey(&k.PublicKey)
		if _, e := parse(string(ssh.MarshalAuthorizedKey(pub))); e != nil {
			t.Fatal(e)
		}
	}
}
func TestRejectSymlinksAndNonregularFiles(t *testing.T) {
	key := public(t)
	for _, kind := range []string{"home-link", "dir-link", "file-link", "directory", "fifo", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			external := filepath.Join(t.TempDir(), "outside")
			os.WriteFile(external, []byte("unchanged"), 0600)
			dir := filepath.Join(home, ".ssh")
			path := filepath.Join(dir, "authorized_keys")
			switch kind {
			case "home-link":
				link := filepath.Join(t.TempDir(), "home")
				os.Symlink(home, link)
				home = link
			case "dir-link":
				os.Symlink(filepath.Dir(external), dir)
			default:
				os.Mkdir(dir, 0700)
				switch kind {
				case "file-link":
					os.Symlink(external, path)
				case "directory":
					os.Mkdir(path, 0700)
				case "fifo":
					unix.Mkfifo(path, 0600)
				case "oversized":
					os.WriteFile(path, bytes.Repeat([]byte{'x'}, maxFile+1), 0600)
				}
			}
			if _, e := Import(home, key); e == nil {
				t.Fatal("unsafe target accepted")
			}
			data, _ := os.ReadFile(external)
			if string(data) != "unchanged" {
				t.Fatal("outside file changed")
			}
		})
	}
}
func TestConcurrentImportsDoNotLoseKeys(t *testing.T) {
	home := t.TempDir()
	keys := make([]string, 12)
	for i := range keys {
		keys[i] = public(t)
	}
	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Go(func() {
			if _, e := Import(home, key); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	data, e := os.ReadFile(filepath.Join(home, ".ssh/authorized_keys"))
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range keys {
		if strings.Count(string(data), key) != 1 {
			t.Fatal("lost or duplicated key")
		}
	}
	files, _ := os.ReadDir(filepath.Join(home, ".ssh"))
	if len(files) != 1 {
		t.Fatal("temporary files remain")
	}
}
