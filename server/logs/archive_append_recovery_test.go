package logs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveAppendPreservesSnapshotWindow(t *testing.T) {
	cases := []struct{ name, first, next string }{
		{"sliding", numberedLines(0, 150), numberedLines(50, 200)},
		{"changed-prefix", "old history\n" + numberedLines(0, 100), "new history\n" + numberedLines(0, 101)},
		{"mid-line-match", "entry 1", "prefixed entry 1\nentry 2"},
		{"extended-last-line", "entry 1", "entry 10\nentry 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NewArchive(filepath.Join(t.TempDir(), "boots"), "11111111-1111-1111-1111-000000000001", 1)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			saveSystem(t, a, tc.first)
			saveSystem(t, a, tc.next)
			got, err := a.Read("current", "system")
			if err != nil || got.Content != tc.next {
				t.Fatalf("archive differs from latest snapshot: err=%v want=%d bytes got=%d bytes", err, len(tc.next), len(got.Content))
			}
		})
	}
}

func TestArchiveIgnoresUncommittedAppendAfterReboot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boots")
	a, err := NewArchive(dir, "11111111-1111-1111-1111-000000000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	first := numberedLines(0, 10)
	saveSystem(t, a, first)
	// Model a crash after some append bytes reached disk but before the
	// corresponding boot.json update, including a partial final entry.
	f, err := os.OpenFile(filepath.Join(dir, "current", "system.log"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("\nuncommitted complete entry\nuncommitted partial"); err != nil {
		t.Fatal(err)
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	a.Close()
	next, err := NewArchive(dir, "11111111-1111-1111-1111-000000000002", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	got, err := next.Read("previous", "system")
	if err != nil || got.Content != first {
		t.Fatalf("uncommitted append exposed after reboot: err=%v want=%q got=%q", err, first, got.Content)
	}
}

func TestArchiveRecoversAppendAfterMetadataFailure(t *testing.T) {
	for _, retryNew := range []bool{true, false} {
		t.Run(map[bool]string{true: "retry-new", false: "return-to-old"}[retryNew], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "boots")
			a, err := NewArchive(dir, "11111111-1111-1111-1111-000000000001", 1)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			first, second := numberedLines(0, 10), numberedLines(0, 20)
			saveSystem(t, a, first)
			blocker := filepath.Join(dir, "current", "boot.json.tmp")
			if err := os.Mkdir(blocker, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(blocker, "block"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := a.Save([]Snapshot{{Boot: "current", Source: "system", State: "ready", Content: second, CollectedAt: 2}}); err == nil {
				t.Fatal("expected metadata failure")
			}
			if a.Healthy() {
				t.Fatal("failed archive reported healthy")
			}
			got, err := a.Read("current", "system")
			if err != nil || got.Content != first {
				t.Errorf("metadata failure exposed uncommitted append: %v", err)
			}
			if err := os.RemoveAll(blocker); err != nil {
				t.Fatal(err)
			}
			want := first
			if retryNew {
				want = second
			}
			saveSystem(t, a, want)
			got, err = a.Read("current", "system")
			if err != nil || got.Content != want {
				t.Fatalf("retry lost or duplicated content: %v", err)
			}
			if !a.Healthy() {
				t.Fatal("successful retry not healthy")
			}
		})
	}
}

func TestArchiveReadsLegacyNonTerminatedSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boots")
	a, err := NewArchive(dir, "11111111-1111-1111-1111-000000000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	first := numberedLines(0, 10)
	saveSystem(t, a, first)
	a.Close()
	// Model the previous release's format, including its unterminated last line.
	path := filepath.Join(dir, "current", "boot.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	delete(record["sources"].(map[string]any)["system"].(map[string]any), "window")
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current", "system.log"), []byte(first), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := NewArchive(dir, "11111111-1111-1111-1111-000000000002", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	got, err := next.Read("previous", "system")
	if err != nil || got.Content != first {
		t.Fatalf("legacy archive lost its last line: %v %q", err, got.Content)
	}
}

func TestArchiveRecoversRewriteWithOldMetadata(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boots")
	a, err := NewArchive(dir, "11111111-1111-1111-1111-000000000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	saveSystem(t, a, numberedLines(0, 150))
	saveSystem(t, a, numberedLines(50, 200))
	blocker := filepath.Join(dir, "current", "boot.json.tmp")
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "block"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	replacement := numberedLines(900000, 10)
	if err := a.Save([]Snapshot{{Boot: "current", Source: "system", State: "ready", Content: replacement, CollectedAt: 2}}); err == nil {
		t.Fatal("expected metadata failure")
	}
	got, err := a.Read("current", "system")
	if err != nil || got.Content != replacement || !got.Truncated {
		t.Fatalf("complete atomic replacement was not recovered: %v %+v", err, got)
	}
	// If the replacement is damaged, recover only complete entries.
	if err := os.Truncate(filepath.Join(dir, "current", "system.log"), int64(len(replacement)-5)); err != nil {
		t.Fatal(err)
	}
	got, err = a.Read("current", "system")
	want := replacement[:strings.LastIndexByte(replacement, '\n')]
	if err != nil || got.Content != want || !got.Truncated {
		t.Fatalf("partial final entry exposed during recovery: %v %+v", err, got)
	}
}

func TestArchiveRejectsInvalidCommittedWindowBounds(t *testing.T) {
	for _, bounds := range []string{
		`{"offset":-1,"length":1,"sha256":"bad"}`,
		`{"offset":9223372036854775807,"length":2,"sha256":"bad"}`,
		`{"offset":0,"length":-1,"sha256":"bad"}`,
		`{"offset":0,"length":2147483647,"sha256":"bad"}`,
	} {
		t.Run(bounds, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "boots")
			a, err := NewArchive(dir, "11111111-1111-1111-1111-000000000001", 1)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			first := numberedLines(0, 10)
			saveSystem(t, a, first)
			path := filepath.Join(dir, "current", "boot.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]json.RawMessage
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			record["sources"] = json.RawMessage(`{"system":{"window":` + bounds + `}}`)
			data, err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := a.Read("current", "system")
			if err != nil || got.Content != first || !got.Truncated {
				t.Fatalf("invalid window recovery: %v %+v", err, got)
			}
		})
	}
}

func TestArchiveAppendFailureForcesRewrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boots")
	a, err := NewArchive(dir, "11111111-1111-1111-1111-000000000001", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	first := numberedLines(0, 10)
	saveSystem(t, a, first)
	if err := os.Remove(filepath.Join(dir, "current", "system.log")); err != nil {
		t.Fatal(err)
	}
	if err := a.Save([]Snapshot{{Boot: "current", Source: "system", State: "ready", Content: numberedLines(0, 20), CollectedAt: 2}}); err == nil {
		t.Fatal("expected append-open failure")
	}
	if a.Healthy() {
		t.Fatal("append failure reported healthy")
	}
	// An old matching digest must not suppress restoration of the missing file.
	saveSystem(t, a, first)
	got, err := a.Read("current", "system")
	if err != nil || got.Content != first || !a.Healthy() {
		t.Fatalf("append failure was not repaired: %v %+v", err, got)
	}
}
