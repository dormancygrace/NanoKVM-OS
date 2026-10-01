package logs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSystemTailBoundsAndRotation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "messages")
	content := strings.Repeat("old message\n", MaxBytes/12+10) + "latest message\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	data, truncated, err := readSystemTail(directory)
	if err != nil || !truncated || len(data) != MaxBytes {
		t.Fatalf("tail: %d %v %v", len(data), truncated, err)
	}
	text, lines, clipped := sanitizeTail(data, truncated)
	if !clipped || lines > MaxLines || len(text) > MaxBytes || !strings.HasSuffix(text, "latest message") {
		t.Fatal("tail lost its bounds or newest line")
	}
	if err := os.Rename(path, path+".0"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, _, err = readSystemTail(directory)
	if err != nil || !strings.HasSuffix(string(data), "new file\n") || !strings.Contains(string(data), "latest message\n") {
		t.Fatalf("rename rotation: %q %v", data, err)
	}
	if err := os.WriteFile(path, []byte("short\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, _, err = readSystemTail(directory)
	if err != nil || !strings.HasSuffix(string(data), "short\n") {
		t.Fatalf("copytruncate: %q %v", data, err)
	}
}

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

func TestBufferWrapAndConcurrentUse(t *testing.T) {
	var buffer Buffer
	_, _ = buffer.Write([]byte("first\n"))
	content := strings.Repeat("m", MaxBytes+300) + "\n"
	_, _ = buffer.Write([]byte(content))
	data, truncated := buffer.tail()
	if !truncated || len(data) != MaxBytes || string(data) != content[len(content)-MaxBytes:] {
		t.Fatal("ring did not retain bounded tail")
	}
	var wait sync.WaitGroup
	for i := 0; i < 10; i++ {
		wait.Go(func() {
			for j := 0; j < 50; j++ {
				_, _ = buffer.Write([]byte("line\n"))
				_, _ = buffer.tail()
			}
		})
	}
	wait.Wait()
}

func TestCollectorCachesSuccessAndFailureForAllClients(t *testing.T) {
	collector := NewCollector()
	collector.application = &Buffer{}
	collector.logDir = t.TempDir()
	now := time.Unix(1000, 0)
	collector.now = func() time.Time { return now }
	calls := 0
	collector.kernel = func(data []byte) (int, error) { calls++; return copy(data, "kernel event\n"), nil }
	var wait sync.WaitGroup
	for i := 0; i < 20; i++ {
		wait.Go(func() {
			snapshot, err := collector.Read("kernel")
			if err != nil || snapshot.Content != "kernel event" {
				t.Errorf("kernel: %+v %v", snapshot, err)
			}
		})
	}
	wait.Wait()
	if calls != 1 {
		t.Fatalf("%d reads instead of one", calls)
	}
	absent, _ := collector.Read("system")
	if absent.State != "unavailable" {
		t.Fatal(absent)
	}
	if err := os.WriteFile(filepath.Join(collector.logDir, "messages"), []byte("new log\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cached, _ := collector.Read("system")
	if cached.State != "unavailable" {
		t.Fatal("failure was not cached")
	}
	now = now.Add(RefreshInterval)
	fresh, _ := collector.Read("system")
	if fresh.Content != "new log" {
		t.Fatal(fresh)
	}
	_, _ = collector.Read("kernel")
	if calls != 2 {
		t.Fatal(calls)
	}
	for _, source := range []string{"../../etc/shadow", "/var/log/messages", "kernel;id", "system\x00"} {
		if _, err := collector.Read(source); err != ErrSource {
			t.Fatalf("source %q was accepted", source)
		}
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

func BenchmarkSanitizeMiB(b *testing.B) {
	line := "[2026-10-01 20:00:00] [info] [network.go:123] DHCP lease of 192.0.2.67 obtained, lease time 21600\n"
	data := []byte(strings.Repeat(line, MaxBytes/len(line)))
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sanitizeTail(data, false)
	}
}

func TestReadRateSurvivesCacheEviction(t *testing.T) {
	collector := NewCollector()
	collector.application = &Buffer{}
	collector.logDir = t.TempDir()
	collector.kernel = func(data []byte) (int, error) { return 0, nil }
	now := time.Unix(1000, 0)
	collector.now = func() time.Time { return now }
	for _, boot := range []string{"current", "previous"} {
		for _, source := range sources {
			if _, err := collector.ReadBoot(source, boot); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(collector.cache) > 3 {
		t.Fatal("cache memory bound")
	}
	if _, err := collector.ReadBoot("system", "older"); err != ErrReadRate {
		t.Fatal("cache cycling bypassed I/O budget", err)
	}
	now = now.Add(RefreshInterval)
	if _, err := collector.ReadBoot("system", "older"); err != nil {
		t.Fatal("budget did not recover", err)
	}
}
