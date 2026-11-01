package logs

import (
	"encoding/json"
	"os"
	"path/filepath"
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
