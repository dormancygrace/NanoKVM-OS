package authn

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countReads counts the account file reads for the rest of the test.
func countReads(t *testing.T) *atomic.Int64 {
	t.Helper()
	var reads atomic.Int64
	original := readAccountFile
	readAccountFile = func(path string) ([]byte, os.FileInfo, error) {
		reads.Add(1)
		return original(path)
	}
	t.Cleanup(func() { readAccountFile = original })
	return &reads
}

// age backdates the file past settleTime, as if it had been written a while
// ago, so the store may cache it. A distinct offset gives each call a
// distinct modification time.
func age(t *testing.T, path string, offset time.Duration) {
	t.Helper()
	when := time.Now().Add(-time.Hour - offset)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func newCachedStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pwd")
	store := NewStore(path)
	if _, ok, err := store.Authenticate("admin", "admin"); err != nil || !ok {
		t.Fatalf("default login: ok=%v err=%v", ok, err)
	}
	if err := store.Create("alice", "alice-password", RoleUser); err != nil {
		t.Fatal(err)
	}
	age(t, path, 0)
	return store, path
}

func TestUnchangedAccountFileIsReadOnce(t *testing.T) {
	store, _ := newCachedStore(t)
	reads := countReads(t)

	alice, err := store.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err = store.ValidateToken("alice", alice.TokenVersion); err != nil {
			t.Fatal(err)
		}
		if _, err = store.List(); err != nil {
			t.Fatal(err)
		}
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("account file read %d times, want 1", got)
	}
}

// A caller that changes what it was given does not change the cached copy.
func TestCachedDatabaseIsNotShared(t *testing.T) {
	store, _ := newCachedStore(t)
	store.mutex.RLock()
	first, err := store.loadLocked(false)
	store.mutex.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	first.Users[0].Username = "mallory"
	first.Users = first.Users[:0]

	users, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "admin" {
		t.Fatalf("cached database was changed through a copy: %+v", users)
	}
}

// A change made from a shell, not through the store, is seen on the next
// read.
func TestExternalAccountFileChangeIsSeen(t *testing.T) {
	store, path := newCachedStore(t)
	alice, err := store.Get("alice")
	if err != nil {
		t.Fatal(err)
	}

	// Disable alice in place, keeping the file's inode.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), `"role": "user",
      "enabled": true`, `"role": "user",
      "enabled": false`, 1)
	if edited == string(data) {
		t.Fatal("test edit did not apply")
	}
	if err = os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	age(t, path, time.Minute)

	if _, err = store.ValidateToken("alice", alice.TokenVersion); err == nil {
		t.Fatal("session survived an account disabled from a shell")
	}
}

// A file written moments ago is read again each time: a second write in the
// same tick of the filesystem's clock might not change its stamp.
func TestFreshlyWrittenAccountFileIsNotCached(t *testing.T) {
	store, path := newCachedStore(t)
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	reads := countReads(t)
	for range 3 {
		if _, err := store.Get("alice"); err != nil {
			t.Fatal(err)
		}
	}
	if got := reads.Load(); got != 3 {
		t.Fatalf("account file read %d times, want 3", got)
	}
}

func TestSaveInvalidatesTheCache(t *testing.T) {
	store, _ := newCachedStore(t)
	alice, err := store.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	if store.cache.Load() == nil {
		t.Fatal("settled account file was not cached")
	}

	if _, err = store.Revoke("alice"); err != nil {
		t.Fatal(err)
	}
	if store.cache.Load() != nil {
		t.Fatal("cache survived the store's own write")
	}
	if _, err = store.ValidateToken("alice", alice.TokenVersion); err == nil {
		t.Fatal("revoked token still valid")
	}
}

func TestRemovedAccountFileDropsTheCache(t *testing.T) {
	store, path := newCachedStore(t)
	admin, err := store.Get("admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ValidateToken("admin", admin.TokenVersion); err == nil {
		t.Fatal("session survived account-file reset")
	}
	if store.cache.Load() != nil {
		t.Fatal("cache survived the account file")
	}
}

func TestFutureAccountTimestampIsNotCached(t *testing.T) {
	store, path := newCachedStore(t)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	reads := countReads(t)
	for range 3 {
		if _, err := store.Get("alice"); err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 3 {
		t.Fatal("future-dated file was cached")
	}
}

func TestSameSizeReplacementInvalidatesCachedToken(t *testing.T) {
	store, path := newCachedStore(t)
	alice, err := store.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := strings.Replace(string(data), `"alice"`, `"blice"`, 1)
	if replaced == string(data) {
		t.Fatal("replacement not applied")
	}
	temp := path + ".replacement"
	if err := os.WriteFile(temp, []byte(replaced), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(temp, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateToken("alice", alice.TokenVersion); err == nil {
		t.Fatal("replaced identity stayed authorized")
	}
	if _, err := store.Get("blice"); err != nil {
		t.Fatal(err)
	}
}
