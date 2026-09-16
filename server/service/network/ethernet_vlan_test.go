package network

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEthernetVLANPersistence(t *testing.T) {
	original := ethernetVLANFile
	ethernetVLANFile = filepath.Join(t.TempDir(), "boot", "eth.vlan")
	t.Cleanup(func() { ethernetVLANFile = original })

	enabled, id, err := readEthernetVLAN()
	if err != nil || enabled || id != 0 {
		t.Fatalf("default VLAN = %t, %d, %v", enabled, id, err)
	}
	if err := writeEthernetVLAN(true, 123); err != nil {
		t.Fatal(err)
	}
	enabled, id, err = readEthernetVLAN()
	if err != nil || !enabled || id != 123 {
		t.Fatalf("saved VLAN = %t, %d, %v", enabled, id, err)
	}
	info, err := os.Stat(ethernetVLANFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("VLAN file permissions = %v, %v", info, err)
	}
	if err := writeEthernetVLAN(false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ethernetVLANFile); !os.IsNotExist(err) {
		t.Fatalf("expected disabled VLAN file to be removed: %v", err)
	}
}

func TestEthernetVLANRejectsInvalidID(t *testing.T) {
	for _, id := range []int{-1, 0, 4095} {
		if err := writeEthernetVLAN(true, id); err == nil {
			t.Fatalf("expected error for VLAN ID %d", id)
		}
	}
	original := ethernetVLANFile
	ethernetVLANFile = filepath.Join(t.TempDir(), "eth.vlan")
	t.Cleanup(func() { ethernetVLANFile = original })
	if err := os.WriteFile(ethernetVLANFile, []byte("100 extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readEthernetVLAN(); err == nil {
		t.Fatal("expected malformed VLAN file to be rejected")
	}
}
