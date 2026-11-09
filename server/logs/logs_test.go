package logs

import (
	"os"
	"path/filepath"
	"strings"

	"testing"

	"golang.org/x/sys/unix"
)

func TestSystemTailRejectsLinksAndSpecialFiles(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(target, []byte("do not disclose\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "messages")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if data, _, err := readSystemTail(directory); err == nil || len(data) > 0 {
		t.Fatal("followed external symlink")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "other"), []byte("inside root\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", path); err != nil {
		t.Fatal(err)
	}
	if data, _, err := readSystemTail(directory); err == nil || len(data) > 0 {
		t.Fatal("followed internal symlink")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if data, _, err := readSystemTail(directory); err == nil || len(data) > 0 {
		t.Fatal("read FIFO")
	}
}

func TestSanitizeCredentialsControlCodesAndPartialEntries(t *testing.T) {
	input := "safe DHCP lease obtained\npassword = multi word hidden-value\nAuthorization: Bearer hidden-value\nhttps://user:hidden-value@example.com\nCookie: nano-kvm-token=hidden-value\napi_key: hidden-value\n-----BEGIN PRIVATE KEY-----\nprivate material\n-----END PRIVATE KEY-----\n" + strings.Repeat("B", 64) + "\n\x1b[31msafe\x1b[0m\npa\x00ssword=hidden-value\n<img src=x onerror=alert(1)>\nincomplete secret"
	text, _, _ := sanitizeTail([]byte(input), false)
	if strings.Contains(text, "hidden-value") || strings.Contains(text, "private material") || strings.Contains(text, strings.Repeat("B", 64)) || strings.Contains(text, "\x1b") || strings.Contains(text, "incomplete") {
		t.Fatalf("unsafe snapshot: %q", text)
	}
	if !strings.Contains(text, "safe DHCP lease obtained") || !strings.Contains(text, "<img src=x onerror=alert(1)>") {
		t.Fatal("ordinary messages were lost")
	}
	text, _, _ = sanitizeTail([]byte("tail of a secret\nsafe complete\n"), true)
	if text != "safe complete" {
		t.Fatal("partial first entry was shown", text)
	}
}

func TestSanitizeOutputBounds(t *testing.T) {
	text, lines, truncated := sanitizeTail([]byte(strings.Repeat("token=x\n", MaxLines+10)), false)
	if !truncated || lines != MaxLines || len(text) > MaxBytes {
		t.Fatalf("bounds: %d %d %v", len(text), lines, truncated)
	}
	text, _, truncated = sanitizeTail([]byte(strings.Repeat("x", 5000)+"\nlast\n"), false)
	if !truncated || !strings.HasSuffix(text, "last") {
		t.Fatal("long-line bound")
	}
}
