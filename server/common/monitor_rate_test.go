package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMonitorRefreshFollowsStreamRate(t *testing.T) {
	for _, c := range []struct {
		height uint16
		fps    int
		want   int
	}{
		{1080, 120, 100}, {1080, 100, 100}, {1080, 90, 100}, {1080, 75, 75}, {1080, 60, 60}, {1080, 50, 60}, {1080, 30, 30}, {1080, 25, 30},
		{1440, 60, 60}, {1440, 55, 60}, {1440, 50, 50}, {1440, 45, 50}, {1440, 40, 40}, {1440, 30, 30}, {1440, 10, 30},
		{720, 120, 120}, {720, 90, 120}, {720, 60, 60}, {720, 31, 60}, {720, 30, 30},
		{2160, 60, 30}, {2160, 30, 30}, {600, 30, 0},
	} {
		if got := MonitorRefreshFor(c.height, c.fps); got != c.want {
			t.Errorf("%dp at %d fps: %d Hz, want %d", c.height, c.fps, got, c.want)
		}
	}
}

func TestMonitorProfileForStreamRate(t *testing.T) {
	dir := t.TempDir()
	old := monitorEDIDDir
	monitorEDIDDir = dir
	defer func() { monitorEDIDDir = old }()
	for _, name := range []string{"NanoKVM-monitor-1080-75.bin", "NanoKVM-monitor-1080-60.bin", "NanoKVM-monitor-1080-30.bin"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := filepath.Base(monitorProfilePathAt(1080, 25)); got != "NanoKVM-monitor-1080-30.bin" {
		t.Fatalf("25 fps: %s", got)
	}
	if got := filepath.Base(monitorProfilePathAt(1080, 50)); got != "NanoKVM-monitor-1080-60.bin" {
		t.Fatalf("50 fps: %s", got)
	}
	// Without a rate profile the single-rate profile remains.
	if got := filepath.Base(monitorProfilePathAt(720, 30)); got != "NanoKVM-monitor-720.bin" {
		t.Fatalf("missing rate profile: %s", got)
	}
}

func TestProgrammedMonitorProfileComparesContent(t *testing.T) {
	dir := t.TempDir()
	oldHash := monitorEDIDHashFile
	monitorEDIDHashFile = filepath.Join(dir, "monitor_edid_sha256")
	defer func() { monitorEDIDHashFile = oldHash }()
	a, b := filepath.Join(dir, "a.bin"), filepath.Join(dir, "b.bin")
	_ = os.WriteFile(a, []byte("same"), 0o644)
	_ = os.WriteFile(b, []byte("other"), 0o644)
	recordMonitorProfileLocked(a)
	if !programmedMonitorProfileLocked(a, 1080) || programmedMonitorProfileLocked(b, 1080) {
		t.Fatal("recorded profile not recognized by content")
	}
	renamed := filepath.Join(dir, "renamed.bin")
	_ = os.WriteFile(renamed, []byte("same"), 0o644)
	if !programmedMonitorProfileLocked(renamed, 1080) {
		t.Fatal("identical bytes under another name must not be rewritten")
	}
}
