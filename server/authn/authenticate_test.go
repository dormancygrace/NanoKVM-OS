package authn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"NanoKVM-Server/utils"

	aesbridge "github.com/mervick/aes-bridge-go"
	"golang.org/x/crypto/bcrypt"
)

func writeTestUsers(t *testing.T, users ...User) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pwd")
	data, err := json.Marshal(database{Version: currentFileVersion, Users: users})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return NewStore(path)
}

// An account stored with the old reversible encryption is upgraded to bcrypt
// on its first successful login, after the lock was released for the check.
func TestAuthenticateUpgradesReversiblePassword(t *testing.T) {
	ciphertext, err := aesbridge.EncryptLegacy("legacy-password", utils.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	store := writeTestUsers(t, User{Username: "owner", PasswordHash: string(ciphertext), Role: RoleAdmin, Enabled: true})

	user, ok, err := store.Authenticate("owner", "legacy-password")
	if err != nil || !ok {
		t.Fatalf("legacy login: ok=%v err=%v", ok, err)
	}
	if user.TokenVersion == 0 || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("legacy-password")) != nil {
		t.Fatalf("password was not upgraded: %+v", user)
	}
	stored, err := store.Get("owner")
	if err != nil || stored.PasswordHash != user.PasswordHash {
		t.Fatalf("upgrade not saved: %+v %v", stored, err)
	}
	if _, ok, _ = store.Authenticate("owner", "wrong-password"); ok {
		t.Fatal("wrong password accepted after the upgrade")
	}
}

func TestAuthenticateRejectsUnknownAndDisabledUsers(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	store := writeTestUsers(t,
		User{Username: "owner", PasswordHash: string(hash), Role: RoleAdmin, Enabled: true},
		User{Username: "off", PasswordHash: string(hash), Role: RoleUser, Enabled: false},
	)
	for _, name := range []string{"nobody", "off"} {
		if _, ok, err := store.Authenticate(name, "correct-password"); ok || err != nil {
			t.Fatalf("%s: ok=%v err=%v", name, ok, err)
		}
	}
	if _, ok, err := store.Authenticate("owner", "correct-password"); !ok || err != nil {
		t.Fatalf("owner: ok=%v err=%v", ok, err)
	}
}
