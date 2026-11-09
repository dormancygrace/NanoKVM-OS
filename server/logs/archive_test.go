package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveRejectsInvalidPathsAndReappliesRedaction(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "boots")
	archive, err := NewArchive(directory, "11111111-1111-1111-1111-111111111111", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if err = archive.Save([]Snapshot{{Boot: "current", Source: "system", State: "ready", Content: "safe line", CollectedAt: 2}}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "current/system.log"), []byte("password=must-not-escape\nsafe line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := archive.Read("current", "system")
	if err != nil || strings.Contains(snapshot.Content, "must-not-escape") || !strings.Contains(snapshot.Content, "safe line") {
		t.Fatal("archive policy", err, snapshot.Content)
	}
	for _, slot := range []string{"../../etc", "current/../older", "/var/log"} {
		if _, err = archive.Read(slot, "system"); err != ErrSource {
			t.Fatal("slot accepted", slot)
		}
	}
	for _, source := range []string{"../../etc/shadow", "system.log", "boot"} {
		if _, err = archive.Read("current", source); err != ErrSource {
			t.Fatal("source accepted", source)
		}
	}
	os.Remove(filepath.Join(directory, "current/system.log"))
	os.Symlink("/etc/passwd", filepath.Join(directory, "current/system.log"))
	if _, err = archive.Read("current", "system"); err == nil {
		t.Fatal("archive symlink read")
	}
}

func (a *Archive) Close() error { return a.root.Close() }
