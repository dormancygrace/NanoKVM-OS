package authn

import (
	"encoding/json"
	"errors"

	"os"
	"path/filepath"

	"testing"
)

func TestCorruptAccountFileFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pwd")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok, err := NewStore(path).Authenticate("admin", "admin")
	if err == nil || ok {
		t.Fatalf("corrupt file must fail closed: ok=%v err=%v", ok, err)
	}
}

func TestUserLifecycleAndTokenRevocation(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "pwd"))
	if _, ok, err := store.Authenticate("admin", "admin"); err != nil || !ok {
		t.Fatalf("default login: ok=%v err=%v", ok, err)
	}
	if err := store.Create("alice", "correct-horse", RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Authenticate("alice", "wrong-password"); err != nil || ok {
		t.Fatalf("wrong password login: ok=%v err=%v", ok, err)
	}
	alice, err := store.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ValidateToken("alice", alice.TokenVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetPasswordAndRunIf("alice", "new-password", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ValidateToken("alice", alice.TokenVersion); err == nil {
		t.Fatal("old token stayed valid after password change")
	}
	if _, ok, err := store.Authenticate("alice", "new-password"); err != nil || !ok {
		t.Fatalf("new password login: ok=%v err=%v", ok, err)
	}

	disabled := false
	if _, _, err = store.Update("admin", "alice", UserPatch{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Authenticate("alice", "new-password"); err != nil || ok {
		t.Fatalf("disabled login: ok=%v err=%v", ok, err)
	}
	enabled := true
	if _, _, err = store.Update("admin", "alice", UserPatch{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err = store.Delete("admin", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get("alice"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("deleted user lookup error = %v", err)
	}
}

func TestLastAdminAndSelfProtection(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "pwd"))
	if _, ok, err := store.Authenticate("admin", "admin"); err != nil || !ok {
		t.Fatalf("default login: ok=%v err=%v", ok, err)
	}
	userRole := RoleUser
	if _, _, err := store.Update("admin", "admin", UserPatch{Role: &userRole}); !errors.Is(err, ErrSelfModification) {
		t.Fatalf("self demotion error = %v", err)
	}
	if err := store.Delete("admin", "admin"); !errors.Is(err, ErrSelfDelete) {
		t.Fatalf("self delete error = %v", err)
	}
	if err := store.Create("second", "second-password", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, _, err := store.Update("second", "admin", UserPatch{Enabled: &disabled}); !errors.Is(err, ErrSystemAccount) {
		t.Fatalf("disable device owner error = %v", err)
	}
	if _, _, err := store.Update("second", "admin", UserPatch{Role: &userRole}); !errors.Is(err, ErrSystemAccount) {
		t.Fatalf("demote device owner error = %v", err)
	}
	if err := store.Delete("second", "admin"); !errors.Is(err, ErrSystemAccount) {
		t.Fatalf("delete device owner error = %v", err)
	}
	if err := store.Delete("admin", "second"); err != nil {
		t.Fatal(err)
	}
}

func TestRenameRevokesTokensAndPreservesDeviceOwner(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "pwd"))
	owner, ok, err := store.Authenticate("admin", "admin")
	if err != nil || !ok {
		t.Fatalf("default login: ok=%v err=%v", ok, err)
	}

	renamed := "owner"
	updated, changed, err := store.Update("admin", "admin", UserPatch{Username: &renamed})
	if err != nil {
		t.Fatalf("rename device owner: %v", err)
	}
	if !changed || updated.Username != renamed || !updated.SystemAccount || updated.TokenVersion == owner.TokenVersion {
		t.Fatalf("unexpected renamed owner: %+v (old token version %d)", updated, owner.TokenVersion)
	}
	if _, err = store.Get("admin"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("old username lookup error = %v", err)
	}
	if _, err = store.ValidateToken("admin", owner.TokenVersion); err == nil {
		t.Fatal("old username session stayed valid after rename")
	}
	if _, err = store.ValidateToken("owner", owner.TokenVersion); err == nil {
		t.Fatal("old token version stayed valid after rename")
	}
	if _, ok, err = store.Authenticate("owner", "admin"); err != nil || !ok {
		t.Fatalf("renamed owner login: ok=%v err=%v", ok, err)
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var mirror legacyAccount
	if err = json.Unmarshal(data, &mirror); err != nil {
		t.Fatal(err)
	}
	if mirror.Username != renamed || mirror.Password == "" {
		t.Fatalf("legacy mirror was not renamed: %+v", mirror)
	}

	userRole := RoleUser
	if _, _, err = store.Update("owner", "owner", UserPatch{Role: &userRole}); !errors.Is(err, ErrSelfModification) {
		t.Fatalf("renamed device owner could demote itself: %v", err)
	}
	if err = store.Delete("owner", "owner"); !errors.Is(err, ErrSelfDelete) {
		t.Fatalf("renamed device owner could delete itself: %v", err)
	}
}

func TestOnlyDeviceOwnerCanRenameDeviceOwner(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "pwd"))
	owner, ok, err := store.Authenticate("admin", "admin")
	if err != nil || !ok {
		t.Fatalf("default login: ok=%v err=%v", ok, err)
	}
	if err = store.Create("second", "second-password", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	second, err := store.Get("second")
	if err != nil {
		t.Fatal(err)
	}

	renamed := "owner"
	if _, _, err = store.Update("second", "admin", UserPatch{Username: &renamed}); !errors.Is(err, ErrSystemAccountRename) {
		t.Fatalf("other admin rename device owner error = %v", err)
	}
	currentOwner, err := store.Get("admin")
	if err != nil {
		t.Fatalf("device owner changed after rejected rename: %v", err)
	}
	if currentOwner.TokenVersion != owner.TokenVersion {
		t.Fatalf("device owner token version changed after rejected rename: got %d want %d", currentOwner.TokenVersion, owner.TokenVersion)
	}
	currentSecond, err := store.Get("second")
	if err != nil {
		t.Fatalf("second admin changed after rejected rename: %v", err)
	}
	if currentSecond.TokenVersion != second.TokenVersion {
		t.Fatalf("second admin token version changed after rejected rename: got %d want %d", currentSecond.TokenVersion, second.TokenVersion)
	}
	if _, err = store.ValidateToken("admin", owner.TokenVersion); err != nil {
		t.Fatalf("device owner session changed after rejected rename: %v", err)
	}
	if _, err = store.ValidateToken("second", second.TokenVersion); err != nil {
		t.Fatalf("second admin session changed after rejected rename: %v", err)
	}

	updated, _, err := store.Update("admin", "admin", UserPatch{Username: &renamed})
	if err != nil {
		t.Fatalf("device owner rename: %v", err)
	}
	if updated.Username != renamed || !updated.SystemAccount {
		t.Fatalf("unexpected renamed device owner: %+v", updated)
	}
}

func TestRemovedAccountFileAndRecreatedUserInvalidateOldVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pwd")
	store := NewStore(path)
	admin, ok, err := store.Authenticate("admin", "admin")
	if err != nil || !ok {
		t.Fatalf("default login: ok=%v err=%v", ok, err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ValidateToken("admin", admin.TokenVersion); err == nil {
		t.Fatal("old admin token survived account-file reset")
	}

	if _, ok, err = store.Authenticate("admin", "admin"); err != nil || !ok {
		t.Fatalf("login after reset: ok=%v err=%v", ok, err)
	}
	if err = store.Create("alice", "alice-password", RoleUser); err != nil {
		t.Fatal(err)
	}
	alice, err := store.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Delete("admin", "alice"); err != nil {
		t.Fatal(err)
	}
	if err = store.Create("alice", "alice-password", RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ValidateToken("alice", alice.TokenVersion); err == nil {
		t.Fatal("old token survived deletion and username reuse")
	}
}
