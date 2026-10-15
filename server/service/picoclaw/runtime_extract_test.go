package picoclaw

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Release archives are written by the standard gzip writer; the extractor
// reads them with klauspost/compress.
func writePicoclawArchive(t *testing.T, files map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "picoclaw_0.3.1/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "picoclaw.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractPicoclawBinary(t *testing.T) {
	binary := make([]byte, 3<<20)
	rand.New(rand.NewSource(1)).Read(binary[:1<<20])
	archive := writePicoclawArchive(t, map[string][]byte{
		"picoclaw_0.3.1/README.md": []byte("readme"),
		"picoclaw_0.3.1/picoclaw":  binary,
	})
	got, err := extractPicoclawBinary(archive, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, binary) {
		t.Fatal("extracted binary differs from the archive")
	}
}

func TestExtractPicoclawBinaryErrors(t *testing.T) {
	missing := writePicoclawArchive(t, map[string][]byte{"picoclaw_0.3.1/README.md": []byte("readme")})
	if _, err := extractPicoclawBinary(missing, t.TempDir()); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("archive without the binary: %v", err)
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.tar.gz")
	if err := os.WriteFile(corrupt, []byte("not a gzip stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractPicoclawBinary(corrupt, t.TempDir()); err == nil {
		t.Fatal("corrupt archive was accepted")
	}
}
