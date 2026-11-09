package authn

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The SHA-1 seed of RFC 6238 appendix B, base32.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPCodeMatchesRFC6238(t *testing.T) {
	// RFC 6238 appendix B, SHA-1, last six of the eight digits.
	for unix, want := range map[int64]string{
		59: "287082", 1111111109: "081804", 1111111111: "050471",
		1234567890: "005924", 2000000000: "279037", 20000000000: "353130",
	} {
		got, err := TOTPCode(rfcSecret, time.Unix(unix, 0))
		if err != nil || got != want {
			t.Errorf("T=%d: %s, %v; want %s", unix, got, err, want)
		}
	}
}

func TestMatchTOTPSkewAndReplay(t *testing.T) {
	now := time.Unix(1111111111, 0)
	step := now.Unix() / totpPeriod
	code := func(offset int64) string {
		c, _ := TOTPCode(rfcSecret, time.Unix((step+offset)*totpPeriod, 0))
		return c
	}
	for offset := int64(-1); offset <= 1; offset++ {
		if got := matchTOTP(rfcSecret, code(offset), now, 0); got != step+offset {
			t.Errorf("offset %d: step %d", offset, got)
		}
	}
	for _, offset := range []int64{-2, 2} {
		if matchTOTP(rfcSecret, code(offset), now, 0) != 0 {
			t.Errorf("offset %d accepted", offset)
		}
	}
	if matchTOTP(rfcSecret, code(0), now, step) != 0 {
		t.Error("a used step was accepted again")
	}
	if matchTOTP(rfcSecret, " "+code(0)[:3]+" "+code(0)[3:], now, 0) != step {
		t.Error("spaces are not ignored")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if matchTOTP(rfcSecret, bad, now, 0) != 0 {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes, err := NewRecoveryCodes()
	if err != nil || len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("%d codes, %d hashes, %v", len(codes), len(hashes), err)
	}
	seen := map[string]bool{}
	for index, code := range codes {
		if len(code) != 14 || code[4] != '-' || code[9] != '-' || seen[code] {
			t.Fatalf("code %q", code)
		}
		seen[code] = true
		if matchRecovery(hashes, strings.ToUpper(code)) != index || matchRecovery(hashes, strings.ReplaceAll(code, "-", "")) != index {
			t.Fatalf("code %q not found", code)
		}
	}
	if matchRecovery(hashes, "aaaa-aaaa-aaaa") >= 0 {
		t.Fatal("an unknown code matched")
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("NanoKVM", "admin", rfcSecret)
	if !strings.HasPrefix(uri, "otpauth://totp/NanoKVM:admin?") || !strings.Contains(uri, "secret="+rfcSecret) ||
		!strings.Contains(uri, "issuer=NanoKVM") {
		t.Fatal(uri)
	}
}

func newTOTPStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "pwd"))
	if err := store.Create("alice", "password1", RoleUser); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStoreSecondFactor(t *testing.T) {
	store := newTOTPStore(t)
	now := time.Unix(1111111111, 0)
	step := now.Unix() / totpPeriod
	codes, hashes, _ := NewRecoveryCodes()
	if err := store.EnableTOTP("alice", rfcSecret, step, hashes); err != nil {
		t.Fatal(err)
	}
	if err := store.EnableTOTP("alice", rfcSecret, step, hashes); !errors.Is(err, ErrTOTPAlreadyEnabled) {
		t.Fatalf("enabled twice: %v", err)
	}
	// The confirming code does not sign in again; the next step does, once.
	current, _ := TOTPCode(rfcSecret, now)
	if _, _, _, err := store.VerifySecondFactor("alice", aliceVersion(t, store), current, now); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatalf("confirming code reused: %v", err)
	}
	later := now.Add(totpPeriod * time.Second)
	next, _ := TOTPCode(rfcSecret, later)
	user, recovery, left, err := store.VerifySecondFactor("alice", aliceVersion(t, store), next, later)
	if err != nil || recovery || left != RecoveryCodeCount || user.Username != "alice" {
		t.Fatalf("%v %v %d %v", user, recovery, left, err)
	}
	if _, _, _, err = store.VerifySecondFactor("alice", aliceVersion(t, store), next, later); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatal("a code was accepted twice")
	}
	// A recovery code works once.
	if _, recovery, left, err = store.VerifySecondFactor("alice", aliceVersion(t, store), codes[3], later); err != nil || !recovery || left != RecoveryCodeCount-1 {
		t.Fatalf("recovery: %v %d %v", recovery, left, err)
	}
	if _, _, _, err = store.VerifySecondFactor("alice", aliceVersion(t, store), codes[3], later); !errors.Is(err, ErrTOTPInvalidCode) {
		t.Fatal("a recovery code was accepted twice")
	}
	// New codes replace the old ones.
	fresh, freshHashes, _ := NewRecoveryCodes()
	if err = store.ReplaceRecoveryCodes("alice", freshHashes); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = store.VerifySecondFactor("alice", aliceVersion(t, store), codes[0], later); err == nil {
		t.Fatal("an old recovery code still works")
	}
	if _, _, _, err = store.VerifySecondFactor("alice", aliceVersion(t, store), fresh[0], later); err != nil {
		t.Fatal(err)
	}
	// The secret stays in the file across a reload; List shows only the flag.
	reloaded := NewStore(store.path)
	users, err := reloaded.List()
	if err != nil || len(users) != 2 || !users[1].TOTPEnabled || users[0].TOTPEnabled {
		t.Fatalf("%+v %v", users, err)
	}
	if was, err := store.DisableTOTP("alice"); err != nil || !was {
		t.Fatalf("disable: %v %v", was, err)
	}
	if _, _, _, err = store.VerifySecondFactor("alice", aliceVersion(t, store), fresh[1], later); !errors.Is(err, ErrTOTPNotEnabled) {
		t.Fatalf("after disable: %v", err)
	}
	if was, err := store.DisableTOTP("alice"); err != nil || was {
		t.Fatalf("second disable: %v %v", was, err)
	}
}

func TestStoreKeepsFileWithoutTOTP(t *testing.T) {
	// Accounts without a second factor are written as before.
	store := newTOTPStore(t)
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "totp") {
		t.Fatalf("totp field written: %s", data)
	}
	var db database
	if err = json.Unmarshal(data, &db); err != nil || len(db.Users) != 2 {
		t.Fatal(err)
	}
}

func TestClonedUserDoesNotShareTOTP(t *testing.T) {
	user := &User{Username: "a", TOTP: &TOTP{Secret: rfcSecret, RecoveryHashes: []string{"x"}}}
	copy := cloneUser(user)
	copy.TOTP.RecoveryHashes[0] = "y"
	copy.TOTP.LastStep = 5
	if user.TOTP.RecoveryHashes[0] != "x" || user.TOTP.LastStep != 0 {
		t.Fatal("clone shares the second factor")
	}
}

func TestDisabledUserFailsSecondFactor(t *testing.T) {
	store := newTOTPStore(t)
	now := time.Unix(1111111111, 0)
	_, hashes, _ := NewRecoveryCodes()
	if err := store.EnableTOTP("alice", rfcSecret, 0, hashes); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, _, err := store.Update("admin", "alice", UserPatch{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	code, _ := TOTPCode(rfcSecret, now)
	if _, _, _, err := store.VerifySecondFactor("alice", aliceVersion(t, store), code, now); err == nil {
		t.Fatal("a disabled account passed the second factor")
	}
}

func aliceVersion(t *testing.T, store *Store) uint64 {
	t.Helper()
	user, err := store.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	return user.TokenVersion
}

// A password proof of an older account version consumes nothing.
func TestSecondFactorRejectsChangedAccount(t *testing.T) {
	store := newTOTPStore(t)
	now := time.Unix(1111111111, 0)
	codes, hashes, _ := NewRecoveryCodes()
	if err := store.EnableTOTP("alice", rfcSecret, 0, hashes); err != nil {
		t.Fatal(err)
	}
	old := aliceVersion(t, store)
	if _, err := store.SetPasswordAndRunIf("alice", "a-new-password", nil, nil); err != nil {
		t.Fatal(err)
	}
	if aliceVersion(t, store) == old {
		t.Fatal("a new password kept the account version")
	}
	code, _ := TOTPCode(rfcSecret, now)
	if _, _, _, err := store.VerifySecondFactor("alice", old, code, now); !errors.Is(err, ErrAccountChanged) {
		t.Fatalf("old version: %v", err)
	}
	if _, _, _, err := store.VerifySecondFactor("alice", old, codes[0], now); !errors.Is(err, ErrAccountChanged) {
		t.Fatalf("old version, recovery code: %v", err)
	}
	// Neither the step nor the recovery code was used up.
	current := aliceVersion(t, store)
	if _, _, _, err := store.VerifySecondFactor("alice", current, code, now); err != nil {
		t.Fatalf("app code after the refusal: %v", err)
	}
	if _, _, left, err := store.VerifySecondFactor("alice", current, codes[0], now); err != nil || left != RecoveryCodeCount-1 {
		t.Fatalf("recovery code after the refusal: %d %v", left, err)
	}
}
