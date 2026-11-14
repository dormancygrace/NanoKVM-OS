package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalKeepsTheNewestAcrossRestarts(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "state", "events.jsonl")
	open := func() *Journal { return &Journal{Path: path, Now: func() time.Time { return now }} }
	j := open()
	for i := 0; i < 2*Max+20; i++ {
		now = now.Add(3 * time.Second) // under the minute's bound
		j.Add(Entry{Kind: "power", Message: "power.on", Details: strings.Repeat("x", 3000)})
	}
	if got := j.Entries(0); len(got) != Max || got[0].Seq != 2*Max+20 || len(got[0].Details) != maxDetails {
		t.Fatalf("kept %d, newest seq %d", len(got), got[0].Seq)
	}
	// The file was rewritten from the newest Max when it held twice Max.
	data, _ := os.ReadFile(path)
	if lines := strings.Count(string(data), "\n"); lines != Max+19 {
		t.Errorf("lines in the file: %d", lines)
	}
	// A cut last line does not cost the rest; the numbering goes on.
	_ = os.WriteFile(path, append(data, []byte(`{"seq":99999,"mess`)...), 0o600)
	j = open()
	j.Add(Entry{Kind: "power", Message: "power.off"})
	if got := j.Entries(2); got[0].Seq != 2*Max+21 || got[0].Message != "power.off" || got[1].Seq != 2*Max+20 {
		t.Errorf("after a restart: %+v", got)
	}
}

func TestJournalBoundsAMinute(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	j := &Journal{Path: filepath.Join(t.TempDir(), "events.jsonl"), Now: func() time.Time { return now }}
	for i := 0; i < maxPerMinute+12; i++ {
		j.Add(Entry{Kind: "security", Message: "security.lockout"})
	}
	now = now.Add(2 * time.Minute)
	j.Add(Entry{Kind: "power", Message: "power.on"})
	got := j.Entries(2)
	if len(j.Entries(0)) != maxPerMinute+2 || got[0].Message != "power.on" || got[1].Message != "journal.dropped" || got[1].Vars["count"] != "12" {
		t.Errorf("%d entries, newest %+v", len(j.Entries(0)), got)
	}
}
