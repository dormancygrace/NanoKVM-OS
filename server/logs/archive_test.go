package logs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveKeepsTwoPreviousBootsAndServiceRestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "boots")
	for boot := 1; boot <= 4; boot++ {
		id := "11111111-1111-1111-1111-" + strings.Repeat("0", 11) + string(rune('0'+boot))
		archive, err := NewArchive(directory, id, int64(boot))
		if err != nil {
			t.Fatal(err)
		}
		snapshot := Snapshot{Boot: "current", Source: "application", State: "ready", Content: strings.Repeat("boot "+string(rune('0'+boot))+" line\n", MaxBytes/12), CollectedAt: int64(boot * 1000)}
		if err = archive.Save([]Snapshot{snapshot}); err != nil {
			t.Fatal(err)
		}
		boots := archive.Boots()
		if len(boots) != min(boot, 3) {
			t.Fatal(boots)
		}
		if boot > 1 {
			previous, err := archive.Read("previous", "application")
			if err != nil || !strings.HasPrefix(previous.Content, "boot "+string(rune('0'+boot-1))) {
				t.Fatalf("previous: %+v %v", previous, err)
			}
		}
		if boot > 2 {
			older, err := archive.Read("older", "application")
			if err != nil || !strings.HasPrefix(older.Content, "boot "+string(rune('0'+boot-2))) {
				t.Fatal("older boot not retained", err)
			}
		}
		archive.Close()
		restarted, err := NewArchive(directory, id, int64(999))
		if err != nil {
			t.Fatal(err)
		}
		if restarted.Boots()[0].StartedAt != int64(boot) {
			t.Fatal("service restart rotated the boot")
		}
		if _, err = restarted.Read("current", "application"); err != nil {
			t.Fatal("restart lost messages", err)
		}
		restarted.Close()
	}
	var size int64
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatal(entries)
	}
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			size += info.Size()
			if info.Mode().Perm() != 0600 {
				t.Errorf("file mode %s: %v", path, info.Mode())
			}
		}
		return nil
	})
	if err != nil || size > 3*(MaxBytes+4096) {
		t.Fatal("disk bound", size, err)
	}
}

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
