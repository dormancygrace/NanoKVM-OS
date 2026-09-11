package timeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChronyTracking(t *testing.T) {
	good := "C0A80101,192.168.1.1,3,1789128483.0,0.0001,-0.0002,0.0003,1.0,0.0,0.1,0.02,0.003,64.0,Normal"
	for _, leap := range []string{"Normal", "Insert second", "Delete second", "Not synchronised"} {
		got, err := parseChronyTracking(strings.Replace(good, "Normal", leap, 1))
		if err != nil || got != (leap != "Not synchronised") {
			t.Fatal(leap, got, err)
		}
	}
	for _, bad := range []string{"506 Cannot talk to daemon", good + "\n" + good, strings.Replace(good, "Normal", "unknown", 1)} {
		if _, err := parseChronyTracking(bad); err == nil {
			t.Fatal("invalid reply accepted")
		}
	}
	if got, _ := parseChronyTracking(strings.Replace(good, "C0A80101", "7F7F0101", 1)); got {
		t.Fatal("local reference accepted")
	}
}
func TestChronyConfigPreservesPolicy(t *testing.T) {
	dir := t.TempDir()
	old := []byte("server old.example iburst\nport 0\ncmdport 0\nmakestep 1.0 3\ndriftfile /var/lib/chrony/drift\n")
	os.WriteFile(filepath.Join(dir, "chrony.conf"), old, 0644)
	calls := 0
	s := Store{Dir: dir, Restart: func() error { calls++; return nil }}
	cfg, err := s.Read()
	if err != nil || cfg.Servers[0] != "old.example" {
		t.Fatal(cfg, err)
	}
	cfg.Servers = []string{"new.example"}
	if err = s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "chrony.conf"))
	if calls != 1 || strings.Contains(string(data), "old.example") || !strings.Contains(string(data), "server new.example iburst") || !strings.Contains(string(data), "cmdport 0") || !strings.Contains(string(data), "makestep 1.0 3") {
		t.Fatal(string(data), calls)
	}
	if _, err = os.Stat(filepath.Join(dir, "ntp.conf")); !os.IsNotExist(err) {
		t.Fatal("wrote ntpd config")
	}
}
