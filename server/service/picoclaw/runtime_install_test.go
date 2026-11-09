package picoclaw

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPicoclawChecksumRequiresMatchingAsset(t *testing.T) {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("archive")))
	for _, text := range []string{digest, digest + "  picoclaw_Linux_arm64.tar.gz", "bad  " + picoclawArchiveName} {
		if _, err := parseSHA256Digest(text, picoclawArchiveName); err == nil {
			t.Errorf("accepted unrelated checksum: %s", text)
		}
	}
	got, err := parseSHA256Digest(strings.Repeat("0", 64)+" other.tar.gz\n"+digest+" *"+picoclawArchiveName, picoclawArchiveName)
	if err != nil || got != digest {
		t.Fatalf("checksum: %q, %v", got, err)
	}
	path := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(path, []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileSHA256(path, digest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileSHA256(path, digest); err == nil {
		t.Fatal("accepted corrupt archive")
	}
}
