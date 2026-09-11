package osupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type testPackageFile struct {
	name     string
	data     []byte
	mode     uint32
	link     string
	preserve bool
}

func fixture(t *testing.T, change func(*Manifest), archiveChange func(*tar.Header), systemFiles ...testPackageFile) (string, ed25519.PublicKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	elf := make([]byte, 20)
	copy(elf, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	binary.LittleEndian.PutUint16(elf[18:], 243)
	files := []testPackageFile{{"NanoKVM-Server", elf, 0755, "", false}, {"web/index.html", []byte("test page"), 0644, "", false}}
	files = append(files, systemFiles...)
	var payload bytes.Buffer
	gz := gzip.NewWriter(&payload)
	tw := tar.NewWriter(gz)
	m := Manifest{Format: 1, Product: "NanoKVM OS", Kind: "application", Arch: "riscv64", Version: "1.0.0-beta.1", Sequence: 1, NativeABI: digest([]byte("abi"))}
	if len(systemFiles) > 0 {
		m.Format = 2
		m.Kind = "system"
		m.SystemBase = digest([]byte("test system foundation"))
	}
	for _, f := range files {
		m.Files = append(m.Files, Entry{Path: f.name, Size: int64(len(f.data)), SHA256: digest(f.data), Mode: f.mode, Link: f.link, Preserve: f.preserve})
		hdr := &tar.Header{Name: f.name, Mode: int64(f.mode), Size: int64(len(f.data)), Typeflag: tar.TypeReg}
		if archiveChange != nil {
			archiveChange(hdr)
		}
		if err = tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err = tw.Write(f.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	m.PayloadBytes = int64(payload.Len())
	m.PayloadSHA256 = digest(payload.Bytes())
	if change != nil {
		change(&m)
	}
	raw, _ := json.Marshal(m)
	sig := ed25519.Sign(key, append([]byte(signatureContext), raw...))
	var out bytes.Buffer
	out.WriteString(Magic)
	binary.Write(&out, binary.BigEndian, uint32(len(raw)))
	out.Write(raw)
	out.Write(sig)
	out.Write(payload.Bytes())
	name := filepath.Join(t.TempDir(), "package.nkos")
	if err = os.WriteFile(name, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return name, pub
}
func TestValidSignedBundle(t *testing.T) {
	name, pub := fixture(t, nil, nil)
	b, err := verifyWithKey(name, pub)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "app")
	if err = Extract(name, b, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "web/index.html"))
	if err != nil || string(data) != "test page" {
		t.Fatal("content not extracted")
	}
}
func TestRejectTamperingAndForeignKeys(t *testing.T) {
	name, pub := fixture(t, nil, nil)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := verifyWithKey(name, other); err == nil {
		t.Fatal("foreign signer accepted")
	}
	original, _ := os.ReadFile(name)
	for _, index := range []int{0, 20, len(original) - 1} {
		mutated := append([]byte(nil), original...)
		mutated[index] ^= 1
		os.WriteFile(name, mutated, 0600)
		if _, err := verifyWithKey(name, pub); err == nil {
			t.Fatalf("tamper accepted at %d", index)
		}
	}
	os.WriteFile(name, []byte("\x1f\x8brenamed original NanoKVM archive"), 0600)
	if _, err := verifyWithKey(name, pub); err == nil {
		t.Fatal("upstream archive accepted")
	}
}
func TestRejectInvalidManifest(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Manifest)
	}{
		{"product", func(m *Manifest) { m.Product = "NanoKVM" }}, {"architecture", func(m *Manifest) { m.Arch = "arm64" }},
		{"traversal", func(m *Manifest) { m.Files[1].Path = "web/../../etc/passwd" }}, {"absolute", func(m *Manifest) { m.Files[1].Path = "/etc/passwd" }},
		{"duplicate", func(m *Manifest) { m.Files[1] = m.Files[0] }}, {"setuid", func(m *Manifest) { m.Files[0].Mode = 04755 }},
		{"size", func(m *Manifest) { m.PayloadBytes++ }}, {"sequence", func(m *Manifest) { m.Sequence = 0 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, key := fixture(t, c.change, nil)
			if _, err := verifyWithKey(name, key); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}
func TestRejectArchiveLinksAndUnlistedPaths(t *testing.T) {
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink} {
		name, key := fixture(t, nil, func(h *tar.Header) { h.Typeflag = kind; h.Size = 0; h.Linkname = "/etc/passwd" })
		b, err := verifyWithKey(name, key)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "app")
		if err = Extract(name, b, dest); err == nil {
			t.Fatal("archive link accepted")
		}
		if _, err = os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("failed staging not removed")
		}
	}
}
func TestGitHubSourceBoundary(t *testing.T) {
	if !ValidAssetURL("https://github.com/dormancygrace/NanoKVM-OS/releases/download/v1/NanoKVM-OS-application.nkos") {
		t.Fatal("own release rejected")
	}
	for _, url := range []string{"https://github.com/sipeed/NanoKVM/releases/download/v1/NanoKVM-OS-application.nkos", "http://github.com/dormancygrace/NanoKVM-OS/releases/download/v1/NanoKVM-OS-application.nkos", "https://github.com.evil.example/dormancygrace/NanoKVM-OS/releases/download/v1/NanoKVM-OS-application.nkos"} {
		if ValidAssetURL(url) {
			t.Fatal("untrusted source accepted")
		}
	}
}

func TestStreamingContentsChecksMatchExtraction(t *testing.T) {
	file, key := fixture(t, nil, nil)
	b, err := verifyWithKey(file, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyContents(file, b); err != nil {
		t.Fatal(err)
	}
	file, key = fixture(t, nil, func(h *tar.Header) { h.Typeflag = tar.TypeSymlink; h.Size = 0; h.Linkname = "/etc/passwd" })
	b, err = verifyWithKey(file, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyContents(file, b); err == nil {
		t.Fatal("archive symlink accepted in streaming validation")
	}
	file, key = fixture(t, func(m *Manifest) { m.Files[1].SHA256 = digest([]byte("wrong content")) }, nil)
	b, err = verifyWithKey(file, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyContents(file, b); err == nil {
		t.Fatal("wrong file hash accepted in streaming validation")
	}
}
