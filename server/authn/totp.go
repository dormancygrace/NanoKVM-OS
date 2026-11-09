package authn

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time passwords (RFC 6238): HMAC-SHA1, six digits, 30-second
// steps, what every authenticator app accepts by default. A code is accepted
// one step early or late for clock drift and phone typing, and never twice:
// the step of the last accepted code is kept with the account.
const (
	totpDigits     = 6
	totpPeriod     = 30
	totpSkew       = 1
	totpSecretSize = 20 // bytes; 32 base32 characters

	// RecoveryCodeCount codes are issued at a time; each signs in once.
	RecoveryCodeCount = 10
	recoveryCodeBytes = 7 // 56 bits: 12 base32 characters, shown as xxxx-xxxx-xxxx
)

var (
	ErrTOTPNotEnabled     = errors.New("two-factor authentication is not enabled")
	ErrTOTPAlreadyEnabled = errors.New("two-factor authentication is already enabled")
	ErrTOTPInvalidCode    = errors.New("invalid authentication code")
	// ErrAccountChanged: the password was checked against an account that
	// has since changed (a new password, sessions revoked).
	ErrAccountChanged = errors.New("the account changed; sign in again")

	base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// TOTP is a user's second factor. Recovery codes are stored as SHA-256
// hashes: 56 random bits need no slow hash, and checking ten bcrypt hashes
// would take ten seconds on the device.
type TOTP struct {
	Secret         string   `json:"secret"` // base32, no padding
	LastStep       int64    `json:"lastStep,omitempty"`
	RecoveryHashes []string `json:"recoveryHashes,omitempty"`
}

func (t *TOTP) clone() *TOTP {
	if t == nil {
		return nil
	}
	copy := *t
	copy.RecoveryHashes = append([]string(nil), t.RecoveryHashes...)
	return &copy
}

func (t *TOTP) valid() bool {
	key, err := base32NoPad.DecodeString(t.Secret)
	return err == nil && len(key) >= 10
}

// NewTOTPSecret returns a random base32 secret.
func NewTOTPSecret() (string, error) {
	key := make([]byte, totpSecretSize)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base32NoPad.EncodeToString(key), nil
}

// TOTPURI is the otpauth:// URI an authenticator app reads from a QR code.
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(totpDigits))
	query.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// totpCode is the code of one time step (RFC 4226 dynamic truncation).
func totpCode(key []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1000000)
}

// TOTPCode is the code of a secret at a time, for tests and tools.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := base32NoPad.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	return totpCode(key, at.Unix()/totpPeriod), nil
}

// matchTOTP returns the step a code belongs to, within the skew around now
// and after lastStep, or 0.
func matchTOTP(secret, code string, now time.Time, lastStep int64) int64 {
	code = normalizeCode(code)
	if len(code) != totpDigits {
		return 0
	}
	key, err := base32NoPad.DecodeString(secret)
	if err != nil {
		return 0
	}
	current := now.Unix() / totpPeriod
	for step := current - totpSkew; step <= current+totpSkew; step++ {
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(key, step)), []byte(code)) == 1 {
			return step
		}
	}
	return 0
}

// normalizeCode drops spaces and dashes that apps and people insert, and
// lower-cases recovery codes.
func normalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == ' ' || r == '-':
			return -1
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return r
	}, strings.TrimSpace(code))
}

// NewRecoveryCodes returns RecoveryCodeCount codes to show once and their hashes.
func NewRecoveryCodes() (codes, hashes []string, err error) {
	for len(codes) < RecoveryCodeCount {
		raw := make([]byte, recoveryCodeBytes)
		if _, err = rand.Read(raw); err != nil {
			return nil, nil, err
		}
		text := strings.ToLower(base32NoPad.EncodeToString(raw))[:12]
		codes = append(codes, text[:4]+"-"+text[4:8]+"-"+text[8:])
		hashes = append(hashes, recoveryHash(text))
	}
	return codes, hashes, nil
}

func recoveryHash(normalized string) string {
	sum := sha256.Sum256([]byte("nanokvm-recovery:" + normalized))
	return hex.EncodeToString(sum[:])
}

// matchRecovery returns the index of the stored hash a recovery code matches, or -1.
func matchRecovery(hashes []string, code string) int {
	code = normalizeCode(code)
	if len(code) != 12 {
		return -1
	}
	want := recoveryHash(code)
	found := -1
	for index, hash := range hashes {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) == 1 {
			found = index
		}
	}
	return found
}
