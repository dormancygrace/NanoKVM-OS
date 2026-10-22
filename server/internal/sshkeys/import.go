// Package sshkeys imports public login keys without replacing existing access.
package sshkeys

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const MaxInput = 32 * 1024
const maxFile = 256 * 1024

var ErrInvalid = errors.New("invalid public SSH keys")

type Result struct {
	Added        int      `json:"added"`
	Skipped      int      `json:"skipped"`
	Fingerprints []string `json:"fingerprints"`
}

type entry struct {
	key  ssh.PublicKey
	line string
}

// Parse each non-comment line independently: ParseAuthorizedKey can otherwise
// skip malformed lines, which would silently import only part of a file.
func parse(input string) ([]entry, error) {
	if len(input) > MaxInput || !utf8.ValidString(input) {
		return nil, ErrInvalid
	}
	var entries []entry
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSuffix(line, "\r")
		for _, c := range line {
			if unicode.IsControl(c) && c != '\t' {
				return nil, ErrInvalid
			}
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return nil, ErrInvalid
		}
		switch key.Type() {
		case ssh.KeyAlgoED25519, ssh.KeyAlgoRSA, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoSKED25519, ssh.KeyAlgoSKECDSA256:
		default:
			return nil, ErrInvalid
		}
		if crypto, ok := key.(ssh.CryptoPublicKey); ok {
			if rsaKey, ok := crypto.CryptoPublicKey().(*rsa.PublicKey); ok && rsaKey.N.BitLen() < 2048 {
				return nil, ErrInvalid
			}
		}
		canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if comment != "" {
			canonical += " " + comment
		}
		entries = append(entries, entry{key, canonical})
		if len(entries) > 100 {
			return nil, ErrInvalid
		}
	}
	if len(entries) == 0 {
		return nil, ErrInvalid
	}
	return entries, nil
}

// Import appends validated public keys atomically. home is fixed by the caller,
// never supplied by the request. Directory descriptors prevent symlink escapes.
func Import(home, input string) (Result, error) {
	result := Result{Fingerprints: []string{}}
	entries, err := parse(input)
	if err != nil {
		return result, err
	}
	h, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return result, err
	}
	defer unix.Close(h)
	if err = unix.Mkdirat(h, ".ssh", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return result, err
	}
	d, err := unix.Openat(h, ".ssh", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return result, err
	}
	defer unix.Close(d)
	// Serialize imports across goroutines and server processes without a lock file.
	if err = unix.Flock(d, unix.LOCK_EX); err != nil {
		return result, err
	}
	defer unix.Flock(d, unix.LOCK_UN)
	fd, err := unix.Openat(d, "authorized_keys", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	var existing []byte
	if err == nil {
		f := os.NewFile(uintptr(fd), "authorized_keys")
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			f.Close()
			return result, errors.New("authorized_keys is not a regular file")
		}
		existing, err = io.ReadAll(io.LimitReader(f, maxFile+1))
		f.Close()
		if err != nil {
			return result, err
		}
		if len(existing) > maxFile {
			return result, errors.New("authorized_keys is too large")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return result, err
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(existing, []byte{'\n'}) {
		key, _, _, _, e := ssh.ParseAuthorizedKey(line)
		if e == nil {
			seen[string(key.Marshal())] = true
		}
	}
	var added strings.Builder
	for _, item := range entries {
		id := string(item.key.Marshal())
		if seen[id] {
			result.Skipped++
			continue
		}
		seen[id] = true
		result.Added++
		result.Fingerprints = append(result.Fingerprints, ssh.FingerprintSHA256(item.key))
		added.WriteString(item.line + "\n")
	}
	if result.Added == 0 {
		return result, nil
	}
	payload := append([]byte(nil), existing...)
	if len(payload) > 0 && payload[len(payload)-1] != '\n' {
		payload = append(payload, '\n')
	}
	payload = append(payload, added.String()...)
	if len(payload) > maxFile {
		return Result{}, errors.New("authorized_keys is too large")
	}
	name := ".authorized_keys-" + rand.Text()
	tmp, err := unix.Openat(d, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return Result{}, err
	}
	defer unix.Unlinkat(d, name, 0)
	f := os.NewFile(uintptr(tmp), name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(payload)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Result{}, err
	}
	if err = unix.Fchmod(d, 0700); err != nil {
		return Result{}, err
	}
	if err = unix.Renameat(d, name, d, "authorized_keys"); err != nil {
		return Result{}, err
	}
	if err = unix.Fsync(d); err != nil {
		return Result{}, err
	}
	if err = unix.Fsync(h); err != nil {
		return Result{}, err
	}
	return result, nil
}
