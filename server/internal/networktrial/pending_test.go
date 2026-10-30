package networktrial

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckIdleRefusesRecordsAndUnknownErrors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "trial.json")
	if err := CheckIdle(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckIdle(path); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	if err := CheckIdle(filepath.Join(path, "child")); err == nil {
		t.Fatal("ignored a non-ENOENT stat failure")
	}
	link := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "absent"), link); err != nil {
		t.Fatal(err)
	}
	if err := CheckIdle(link); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
}

func TestCheckIdleWaitsForAllBackgroundWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	first, second := Begin(), Begin()
	t.Cleanup(first)
	t.Cleanup(second)
	if err := CheckIdle(path); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	first()
	first()
	if err := CheckIdle(path); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	second()
	if err := CheckIdle(path); err != nil {
		t.Fatal(err)
	}
}
