package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func numberedLines(first, count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("Oct 14 12:00:00 kvm daemon.info test[1]: entry %07d", first+i)
	}
	return strings.Join(lines, "\n")
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func saveSystem(t *testing.T, archive *Archive, content string) {
	t.Helper()
	snapshot := Snapshot{Boot: "current", Source: "system", State: "ready", Content: content, CollectedAt: 1}
	if err := archive.Save([]Snapshot{snapshot}); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveAppendsNewLines(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "boots")
	const boot = "11111111-1111-1111-1111-000000000001"
	archive, err := NewArchive(directory, boot, 1)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "current", "system.log")
	first := numberedLines(0, 100)
	saveSystem(t, archive, first)
	written := inode(t, file)

	// Growth appends to the same file.
	second := numberedLines(0, 150)
	saveSystem(t, archive, second)
	if inode(t, file) != written {
		t.Fatal("growth rewrote the file instead of appending")
	}
	if data, _ := os.ReadFile(file); string(data) != second {
		t.Fatal("appended file differs from the snapshot")
	}

	// A sliding window (the head dropped, new lines added) appends too. The
	// file then holds more than the snapshot, but reads return the snapshot.
	third := numberedLines(50, 200)
	saveSystem(t, archive, third)
	if inode(t, file) != written {
		t.Fatal("a sliding window rewrote the file")
	}
	if data, _ := os.ReadFile(file); string(data) != numberedLines(0, 250) {
		t.Fatal("sliding window did not append exactly the new lines")
	}

	// Content without the previous end is rewritten.
	unrelated := numberedLines(900000, 10)
	saveSystem(t, archive, unrelated)
	if data, _ := os.ReadFile(file); string(data) != unrelated {
		t.Fatal("unrelated content was not rewritten")
	}
	archive.Close()

	// A service restart within the same boot rewrites on its first save.
	restarted, err := NewArchive(directory, boot, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := inode(t, file)
	saveSystem(t, restarted, unrelated+"\n"+numberedLines(900010, 5))
	if inode(t, file) == before {
		t.Fatal("first save after a restart appended to a file of unknown origin")
	}
	restarted.Close()

	// After a reboot the previous boot shows the newest lines, as before.
	next, err := NewArchive(directory, "11111111-1111-1111-1111-000000000002", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	previous, err := next.Read("previous", "system")
	if err != nil || previous.Content != unrelated+"\n"+numberedLines(900010, 5) {
		t.Fatalf("previous boot: %v %q", err, previous.Content)
	}
}

func TestArchiveRewritesAtTheFileLimit(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "boots")
	archive, err := NewArchive(directory, "11111111-1111-1111-1111-000000000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "current", "system.log")
	// Each entry is 53 bytes with its newline: a 1 MiB window holds about
	// 19800 of them. Advance the window until the file passes the limit.
	const window = 19000
	var last string
	rewrites := 0
	written := uint64(0)
	for start := 0; start <= 3*window; start += 2000 {
		last = numberedLines(start, window)
		saveSystem(t, archive, last)
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > archiveFileLimit {
			t.Fatal("archive file exceeds its limit", info.Size())
		}
		if now := inode(t, file); now != written {
			rewrites++
			written = now
		}
	}
	if rewrites < 2 || rewrites > 4 {
		t.Fatal("unexpected number of rewrites", rewrites)
	}
	archive.Close()
	next, err := NewArchive(directory, "11111111-1111-1111-1111-000000000002", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	previous, err := next.Read("previous", "system")
	if err != nil || !strings.HasSuffix(previous.Content, last[len(last)-appendAnchorBytes:]) {
		t.Fatal("previous boot lost the newest lines", err)
	}
	if len(previous.Content) > MaxBytes || previous.Lines > MaxLines {
		t.Fatal("read exceeds the response bounds", len(previous.Content), previous.Lines)
	}
}

func TestArchiveRewritesWhenTheAnchorRepeats(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "boots")
	archive, err := NewArchive(directory, "11111111-1111-1111-1111-000000000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	file := filepath.Join(directory, "current", "system.log")
	redacted := func(count int) string {
		return strings.TrimSuffix(strings.Repeat(redactedLine+"\n", count), "\n")
	}
	// The last 4 KiB is nothing but redacted lines, so it occurs more than
	// once in a snapshot that ends with even more of them.
	first := numberedLines(0, 10) + "\n" + redacted(150)
	saveSystem(t, archive, first)
	second := first + "\n" + redacted(150)
	saveSystem(t, archive, second)
	if data, _ := os.ReadFile(file); string(data) != second {
		t.Fatal("a repeated anchor lost lines")
	}
}
