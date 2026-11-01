package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMonitorProfileIDsAndSizes(t *testing.T) {
	cases := []struct {
		id            uint16
		width, height uint16
		aspect        string
		need          string
	}{
		{0, 0, 0, MonitorAspect169, ""},
		{720, 1280, 720, MonitorAspect169, ""},
		{1080, 1920, 1080, MonitorAspect169, ""},
		{1440, 2560, 1440, MonitorAspect169, "qhd"},
		{2160, 3840, 2160, MonitorAspect169, "uhd"},
		{1200, 1920, 1200, MonitorAspect1610, "qhd"},
		{1600, 2560, 1600, MonitorAspect1610, "uhd"},
		{2560, 2560, 1080, MonitorAspect219, "qhd"},
		{3440, 3440, 1440, MonitorAspect219, "uhd"},
	}
	for _, c := range cases {
		width, height := MonitorSize(c.id)
		if width != c.width || height != c.height || MonitorAspect(c.id) != c.aspect || monitorPoolNeed(c.id) != c.need {
			t.Errorf("%d: %dx%d %s %q", c.id, width, height, MonitorAspect(c.id), monitorPoolNeed(c.id))
		}
		if !ValidMonitorProfile(int(c.id)) {
			t.Errorf("%d not valid", c.id)
		}
	}
	// The stream limit 600 and portrait sizes are not landscape monitor ids.
	for _, id := range []int{600, 1920, 1280, -1, 70000} {
		if ValidMonitorProfile(id) {
			t.Errorf("%d accepted", id)
		}
	}
	if len(MonitorProfileIDs) != len(cases) {
		t.Errorf("ids %v", MonitorProfileIDs)
	}
}

// A wider id saved under an LT6911UXC reads as Auto under another receiver.
func TestSavedWiderMonitorNeedsTheUXCReceiver(t *testing.T) {
	old := monitorResolutionFile
	monitorResolutionFile = filepath.Join(t.TempDir(), "monitor_resolution")
	t.Cleanup(func() { monitorResolutionFile = old })
	if err := os.WriteFile(monitorResolutionFile, []byte("3440"), 0o644); err != nil {
		t.Fatal(err)
	}
	selectBoard(t, "pcie", "ux")
	if got := savedMonitorResolutionLocked(); got != 3440 {
		t.Fatalf("UXC: %d", got)
	}
	if got := SelectedMonitorResolution(); got != 3440 {
		t.Fatalf("UXC, public getter: %d", got)
	}
	selectBoard(t, "alpha", "c")
	if got := savedMonitorResolutionLocked(); got != 0 {
		t.Fatalf("LT6911C: %d, want Auto", got)
	}
	if got := SelectedMonitorResolution(); got != 0 {
		t.Fatalf("LT6911C, public getter: %d, want Auto", got)
	}
	// A Cube or Lite with an LT6911UXC takes every wider profile (with a
	// power cycle); an LT6911C or LT6911D takes 1920x1200 and 2560x1080.
	selectBoard(t, "alpha", "ux")
	if got := savedMonitorResolutionLocked(); got != 3440 {
		t.Fatalf("Cube with UXC: %d", got)
	}
	if err := os.WriteFile(monitorResolutionFile, []byte("1200"), 0o644); err != nil {
		t.Fatal(err)
	}
	selectBoard(t, "alpha", "c")
	if got := savedMonitorResolutionLocked(); got != 1200 {
		t.Fatalf("1920x1200 under LT6911C: %d", got)
	}
	if err := os.WriteFile(monitorResolutionFile, []byte("1080"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := savedMonitorResolutionLocked(); got != 1080 {
		t.Fatalf("16:9 under LT6911C: %d", got)
	}
}
