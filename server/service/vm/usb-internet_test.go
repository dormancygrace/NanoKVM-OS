package vm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUSBInternetPersistAndRollback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usb.internet")
	if err := saveUSBInternet(dir, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("not persisted", err)
	}
	// Repeated enable is still a reconciliation, not a toggle.
	calls := 0
	if err := saveUSBInternet(dir, true, func() error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	if err := saveUSBInternet(dir, false, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("not disabled", err)
	}
	calls = 0
	err := saveUSBInternet(dir, true, func() error {
		calls++
		if calls == 1 {
			return errors.New("runtime")
		}
		return nil
	})
	if err == nil || calls != 2 {
		t.Fatal("missing rollback", err, calls)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed enable remained saved", err)
	}
	// Restore the exact flag data on a failed disable, including custom metadata.
	if err = os.WriteFile(path, []byte("metadata\n"), 0644); err != nil {
		t.Fatal(err)
	}
	calls = 0
	err = saveUSBInternet(dir, false, func() error {
		calls++
		if calls == 1 {
			return errors.New("runtime")
		}
		return nil
	})
	data, _ := os.ReadFile(path)
	if err == nil || string(data) != "metadata\n" {
		t.Fatal(err, string(data))
	}
}
