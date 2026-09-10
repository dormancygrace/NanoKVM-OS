// Package osupdate implements the signed NanoKVM OS application update format.
package osupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
)

const Magic = "NKOSAPP1"
const MaxBundle int64 = 96 << 20
const MaxExpanded int64 = 192 << 20
const signatureContext = "NanoKVM OS application update v1\n"

//go:embed release-ed25519.pub.pem
var publicPEM []byte

type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}
type Manifest struct {
	Format        int     `json:"format"`
	Product       string  `json:"product"`
	Kind          string  `json:"kind"`
	Arch          string  `json:"arch"`
	Version       string  `json:"version"`
	Sequence      uint64  `json:"sequence"`
	NativeABI     string  `json:"native_abi"`
	PayloadBytes  int64   `json:"payload_bytes"`
	PayloadSHA256 string  `json:"payload_sha256"`
	Files         []Entry `json:"files"`
}
type Bundle struct {
	Manifest Manifest
	Offset   int64
	ID       string
}

var digestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

func PublicKey() (ed25519.PublicKey, error) {
	block, _ := pem.Decode(publicPEM)
	if block == nil {
		return nil, errors.New("missing release key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("invalid release key")
	}
	return pub, nil
}
func HashFile(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// NativeABI binds an app-only package to the exact installed native library set.
func NativeABI(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var rows []string
	for _, e := range entries {
		if e.IsDir() {
			return "", errors.New("unexpected native directory")
		}
		info, err := e.Info()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("native library is not regular")
		}
		sum, err := HashFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", err
		}
		rows = append(rows, e.Name()+":"+sum+"\n")
	}
	if len(rows) == 0 {
		return "", errors.New("empty native library set")
	}
	sort.Strings(rows)
	h := sha256.New()
	for _, row := range rows {
		io.WriteString(h, row)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func validPath(name string) bool {
	return name != "" && path.Clean(name) == name && !bytes.ContainsAny([]byte(name), "\\\x00") && (name == "NanoKVM-Server" || (len(name) > 4 && name[:4] == "web/"))
}
func validateManifest(m Manifest) error {
	if m.Format != 1 || m.Product != "NanoKVM OS" || m.Kind != "application" || m.Arch != "riscv64" {
		return errors.New("not a NanoKVM OS application package")
	}
	if !versionRE.MatchString(m.Version) || m.Sequence == 0 || !digestRE.MatchString(m.NativeABI) || !digestRE.MatchString(m.PayloadSHA256) || m.PayloadBytes < 1 || m.PayloadBytes > MaxBundle {
		return errors.New("invalid update manifest")
	}
	if len(m.Files) < 2 || len(m.Files) > 4096 {
		return errors.New("invalid file count")
	}
	seen := map[string]bool{}
	var total int64
	for _, e := range m.Files {
		if !validPath(e.Path) || seen[e.Path] || e.Size < 0 || e.Size > 64<<20 || !digestRE.MatchString(e.SHA256) {
			return errors.New("invalid or duplicate package path")
		}
		expected := uint32(0644)
		if e.Path == "NanoKVM-Server" {
			expected = 0755
		}
		if e.Mode != expected {
			return errors.New("invalid file permissions")
		}
		seen[e.Path] = true
		total += e.Size
	}
	if total > MaxExpanded || !seen["NanoKVM-Server"] || !seen["web/index.html"] {
		return errors.New("incomplete or oversized application")
	}
	return nil
}
func Verify(file string) (*Bundle, error) {
	key, err := PublicKey()
	if err != nil {
		return nil, err
	}
	return verifyWithKey(file, key)
}
func verifyWithKey(file string, key ed25519.PublicKey) (*Bundle, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxBundle {
		return nil, errors.New("package too large")
	}
	var prefix [12]byte
	if _, err = io.ReadFull(f, prefix[:]); err != nil {
		return nil, errors.New("invalid NanoKVM OS package")
	}
	if string(prefix[:8]) != Magic {
		return nil, errors.New("original NanoKVM and unsigned archives are not supported")
	}
	length := binary.BigEndian.Uint32(prefix[8:])
	if length == 0 || length > 1<<20 {
		return nil, errors.New("invalid manifest length")
	}
	raw := make([]byte, length)
	sig := make([]byte, ed25519.SignatureSize)
	if _, err = io.ReadFull(f, raw); err != nil {
		return nil, err
	}
	if _, err = io.ReadFull(f, sig); err != nil {
		return nil, err
	}
	if !ed25519.Verify(key, append([]byte(signatureContext), raw...), sig) {
		return nil, errors.New("NanoKVM OS release signature is invalid")
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return nil, err
	}
	if err = validateManifest(m); err != nil {
		return nil, err
	}
	offset := int64(12 + len(raw) + len(sig))
	if info.Size() != offset+m.PayloadBytes {
		return nil, errors.New("truncated package or trailing data")
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return nil, err
	}
	if hex.EncodeToString(h.Sum(nil)) != m.PayloadSHA256 {
		return nil, errors.New("update payload checksum mismatch")
	}
	id, err := HashFile(file)
	if err != nil {
		return nil, err
	}
	return &Bundle{Manifest: m, Offset: offset, ID: id}, nil
}

// Extract accepts only manifest-listed regular files; it never executes content.
func Extract(file string, b *Bundle, dest string) error {
	if err := os.Mkdir(dest, 0700); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dest)
		}
	}()
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Seek(b.Offset, io.SeekStart); err != nil {
		return err
	}
	gz, err := gzip.NewReader(io.LimitReader(f, b.Manifest.PayloadBytes))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	entries := map[string]Entry{}
	for _, e := range b.Manifest.Files {
		entries[e.Path] = e
	}
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		e, exists := entries[hdr.Name]
		if !exists || seen[hdr.Name] || hdr.Typeflag != tar.TypeReg || hdr.Size != e.Size || hdr.Mode != int64(e.Mode) || len(hdr.PAXRecords) > 0 {
			return errors.New("unexpected archive entry")
		}
		seen[hdr.Name] = true
		target := filepath.Join(dest, filepath.FromSlash(e.Path))
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(e.Mode))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(out, h), tr)
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
			return fmt.Errorf("file checksum mismatch: %s", e.Path)
		}
	}
	if len(seen) != len(entries) {
		return errors.New("missing archive entries")
	}
	// Consume gzip trailer so a bad CRC or additional expanded data is rejected.
	extra, err := io.CopyN(io.Discard, gz, 1)
	if extra != 0 || err != io.EOF {
		return errors.New("unexpected data after archive")
	}
	if err = CheckExecutable(filepath.Join(dest, "NanoKVM-Server")); err != nil {
		return err
	}
	ok = true
	return nil
}
func CheckExecutable(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	var h [20]byte
	if _, err = io.ReadFull(f, h[:]); err != nil {
		return err
	}
	if !bytes.Equal(h[:6], []byte{0x7f, 'E', 'L', 'F', 2, 1}) || binary.LittleEndian.Uint16(h[18:]) != 243 {
		return errors.New("server is not RISC-V ELF64")
	}
	return nil
}
