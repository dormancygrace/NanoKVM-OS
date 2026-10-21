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

func TestWiderMonitorProfileFiles(t *testing.T) {
	for id, want := range map[uint16]string{
		1080: "NanoKVM-monitor-1080-90.bin",
		1200: "NanoKVM-monitor-1920x1200-90.bin",
		2560: "NanoKVM-monitor-2560x1080-90.bin",
		3440: "NanoKVM-monitor-3440x1440-90.bin",
	} {
		if got := filepath.Base(monitorRateProfilePath(id, 90)); got != want {
			t.Errorf("%d: %s, want %s", id, got, want)
		}
	}
	// The wider profiles exist per rate; the fastest stands for the profile.
	selectBoard(t, "pcie", "ux")
	if got := filepath.Base(monitorProfilePath(1600)); got != "NanoKVM-monitor-2560x1600-50.bin" {
		t.Errorf("1600 single profile %s", got)
	}
	for id, rates := range widerMonitorRates {
		for i := 1; i < len(rates); i++ {
			if rates[i] >= rates[i-1] {
				t.Errorf("%d rates %v not fastest first", id, rates)
			}
		}
		if limit := CaptureRateLimit(int(widerMonitorProfiles[id].Width), int(widerMonitorProfiles[id].Height)); rates[0] > limit {
			t.Errorf("%d: fastest rate %d above the capture limit %d", id, rates[0], limit)
		}
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
	selectBoard(t, "alpha", "c")
	if got := savedMonitorResolutionLocked(); got != 0 {
		t.Fatalf("LT6911C: %d, want Auto", got)
	}
	if err := os.WriteFile(monitorResolutionFile, []byte("1080"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := savedMonitorResolutionLocked(); got != 1080 {
		t.Fatalf("16:9 under LT6911C: %d", got)
	}
}
