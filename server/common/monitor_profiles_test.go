package common

import (
	"fmt"
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

// Which receiver takes which 16:10 and 21:9 profile, at which rates, and the
// file it writes (the fastest rate unless the monitor follows the stream).
func TestWiderMonitorProfilesPerReceiver(t *testing.T) {
	all := map[uint16][]int{1200: {60, 50, 30}, 1600: {50, 30}, 2560: {60, 50, 30}, 3440: {40, 30}}
	slow := map[uint16][]int{1200: {50, 30}, 2560: {30}}
	for _, tc := range []struct {
		board, chip string
		want        map[uint16][]int
	}{
		{"pcie", "ux", all}, {"alpha", "ux", all}, {"beta", "ux", all},
		{"pcie", "d", slow}, {"alpha", "c", slow}, {"beta", "d", slow},
		{"", "ux", nil}, {"pcie", "c", nil},
	} {
		selectBoard(t, tc.board, tc.chip)
		for _, id := range []uint16{1200, 1600, 2560, 3440} {
			rates, offered := tc.want[id]
			if WiderMonitorProfileOffered(id) != offered {
				t.Errorf("%s/%s %d: offered %v", tc.board, tc.chip, id, !offered)
				continue
			}
			if !offered {
				continue
			}
			if got := MonitorRates(id); fmt.Sprint(got) != fmt.Sprint(rates) {
				t.Errorf("%s/%s %d: rates %v, want %v", tc.board, tc.chip, id, got, rates)
			}
			want := monitorRateProfilePath(id, rates[0])
			if got := monitorProfilePath(id); got != want {
				t.Errorf("%s/%s %d: profile %s, want %s", tc.board, tc.chip, id, got, want)
			}
		}
		for _, id := range []uint16{0, 720, 1080} {
			if WiderMonitorProfileOffered(id) {
				t.Errorf("16:9 %d reported as a wider profile", id)
			}
		}
	}
}
