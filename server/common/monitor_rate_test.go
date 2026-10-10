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

// selectBoard makes the monitor code see a board: hw and the HDMI chip.
func selectBoard(t *testing.T, hw, chip string) {
	t.Helper()
	dir := t.TempDir()
	oldBoard, oldChip := monitorBoardFile, monitorHDMIChipFile
	monitorBoardFile, monitorHDMIChipFile = filepath.Join(dir, "hw"), filepath.Join(dir, "hdmi_version")
	t.Cleanup(func() { monitorBoardFile, monitorHDMIChipFile = oldBoard, oldChip })
	for path, value := range map[string]string{monitorBoardFile: hw, monitorHDMIChipFile: chip} {
		if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Automatic policy. At run time Automatic is the strict 1080p rate profile of
// the stream rate, never a profile that lists a slower fallback beside the
// preferred rate. The static NanoKVM-final-video-profiles.bin (1080p100, also
// strict) is used only where no rate profile applies.
func TestAutomaticMonitorProfilePolicy(t *testing.T) {
	selectBoard(t, "pcie", "ux")
	dir := t.TempDir()
	old := monitorEDIDDir
	monitorEDIDDir = dir
	t.Cleanup(func() { monitorEDIDDir = old })
	write := func(names ...string) {
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := autoMonitorHeight(); got != 1080 {
		t.Fatalf("Automatic is %dp, want 1080", got)
	}
	if got := MonitorRates(0); len(got) != 4 || got[0] != 100 || got[3] != 30 {
		t.Fatalf("Automatic rates %v, want 100 75 60 30", got)
	}
	// Static Automatic file: the final profile (1080p100), not the stock EDID.
	if got := filepath.Base(monitorProfilePath(0)); got != "NanoKVM-final-video-profiles.bin" {
		t.Fatalf("static Automatic profile: %s", got)
	}
	// No rate profiles installed: the static file.
	if got := filepath.Base(monitorProfilePathAt(0, 60)); got != "NanoKVM-final-video-profiles.bin" {
		t.Fatalf("no rate profile: %s", got)
	}
	write("NanoKVM-monitor-1080-100.bin", "NanoKVM-monitor-1080-75.bin",
		"NanoKVM-monitor-1080-60.bin", "NanoKVM-monitor-1080-30.bin")
	for _, c := range []struct {
		fps  int
		want string
	}{
		{120, "NanoKVM-monitor-1080-100.bin"}, {100, "NanoKVM-monitor-1080-100.bin"},
		{90, "NanoKVM-monitor-1080-100.bin"}, {75, "NanoKVM-monitor-1080-75.bin"},
		{60, "NanoKVM-monitor-1080-60.bin"}, {50, "NanoKVM-monitor-1080-60.bin"},
		{30, "NanoKVM-monitor-1080-30.bin"}, {25, "NanoKVM-monitor-1080-30.bin"},
	} {
		if got := filepath.Base(monitorProfilePathAt(0, c.fps)); got != c.want {
			t.Errorf("Automatic at %d fps: %s, want %s", c.fps, got, c.want)
		}
		// Automatic selects exactly what an explicit 1080p setting selects.
		if auto, explicit := monitorProfilePathAt(0, c.fps), monitorProfilePathAt(1080, c.fps); auto != explicit {
			t.Errorf("Automatic at %d fps: %s, explicit 1080p: %s", c.fps, auto, explicit)
		}
	}
}

func TestAutomaticMonitorProfileOnOtherBoards(t *testing.T) {
	old := monitorEDIDDir
	monitorEDIDDir = t.TempDir()
	t.Cleanup(func() { monitorEDIDDir = old })
	// No live EDID writes: Automatic is the stock receiver profile.
	selectBoard(t, "pcie", "c")
	if got := filepath.Base(monitorProfilePathAt(0, 100)); got != "NanoKVM-stock.bin" {
		t.Fatalf("stock Automatic: %s", got)
	}
	if got := MonitorRates(0); len(got) != 0 {
		t.Fatalf("stock Automatic rates %v", got)
	}
	// Cube keeps its single 60 Hz profile at every stream rate.
	selectBoard(t, "alpha", "ux")
	for _, fps := range []int{100, 60, 30} {
		if got := filepath.Base(monitorProfilePathAt(0, fps)); got != "NanoKVM-cube-monitor-1080.bin" {
			t.Fatalf("Cube Automatic at %d fps: %s", fps, got)
		}
	}
	if got := MonitorRates(0); len(got) != 1 || got[0] != 60 {
		t.Fatalf("Cube Automatic rates %v", got)
	}
}
