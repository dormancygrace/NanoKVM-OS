package common

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func firstStartBackend(hardware, powerCycle bool, applied *[]string, fail error) monitorSettingsBackend {
	return monitorSettingsBackend{
		requireHardware: func() error {
			if !hardware {
				return errors.New("EDID programming is unsupported")
			}
			return nil
		},
		portraitSupported: func(uint16) bool { return true },
		powerCycle:        func() bool { return powerCycle },
		followsRefresh:    func() bool { return false },
		apply: func(path string) error {
			*applied = append(*applied, path)
			return fail
		},
		read: func() ([]byte, error) { return testEDID(1920, 280, 1080, 45, 14850, 16), nil },
	}
}

func TestProgramMonitorProfileOnFirstStart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		recorded   bool
		hardware   bool
		powerCycle bool
		writes     int
	}{
		{"after an installation", false, true, false, 1},
		{"profile recorded", true, true, false, 0},
		{"no programmable EDID", false, false, false, 0},
		{"Cube needs a power cycle", false, true, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, 1080, false, 1920)
			if tc.recorded {
				if err := os.WriteFile(monitorEDIDHashFile, []byte("0\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var applied []string
			path, err := programMonitorProfileOnFirstStartLocked(firstStartBackend(tc.hardware, tc.powerCycle, &applied, nil), poolUHD)
			if err != nil || len(applied) != tc.writes {
				t.Fatalf("error %v, applied %v", err, applied)
			}
			if tc.writes == 0 && path != "" {
				t.Fatalf("reported %q without programming", path)
			}
			if tc.writes == 1 && (path != applied[0] || path != monitorProfilePathAt(1080, GetScreen().FPS)) {
				t.Fatalf("reported %q, applied %v", path, applied)
			}
		})
	}
}

// The saved portrait profile is the one advertised, as in applyMonitorSettings.
func TestProgramMonitorProfileOnFirstStartPortrait(t *testing.T) {
	monitorState(t, 1080, true, 1920)
	var applied []string
	if _, err := programMonitorProfileOnFirstStartLocked(firstStartBackend(true, false, &applied, nil), poolUHD); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != portraitProfilePathAt(1920, GetScreen().FPS) {
		t.Fatalf("applied %v", applied)
	}
}

// A failed write records nothing, so the next start tries again.
func TestProgramMonitorProfileOnFirstStartRetriesAfterFailure(t *testing.T) {
	monitorState(t, 1080, false, 1920)
	var applied []string
	path, err := programMonitorProfileOnFirstStartLocked(firstStartBackend(true, false, &applied, errors.New("EDID write failed")), poolUHD)
	if err == nil || path != "" {
		t.Fatalf("path %q, error %v", path, err)
	}
	if _, err := os.Stat(monitorEDIDHashFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a profile is recorded after a failed write: %v", err)
	}
	if _, err := programMonitorProfileOnFirstStartLocked(firstStartBackend(true, false, &applied, nil), poolUHD); err != nil || len(applied) != 2 {
		t.Fatalf("error %v, applied %v", err, applied)
	}
}

// A failed fit leaves the oversized saved setting intact; first-start sync
// must not turn it back into an advertised HDMI mode.
func TestProgramMonitorProfileOnFirstStartRejectsIncompatibleState(t *testing.T) {
	for _, tc := range []struct {
		name              string
		height            int
		portrait          bool
		size              int
		pool              VideoPool
		portraitSupported bool
	}{
		{"QHD under FHD", 1440, false, 1920, poolFHD, true},
		{"UHD under QHD", 2160, false, 1920, poolQHD, true},
		{"portrait under FHD", 1080, true, 1920, poolFHD, true},
		{"portrait hardware unavailable", 1080, true, 1920, poolQHD, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, tc.height, tc.portrait, tc.size)
			var applied []string
			hw := firstStartBackend(true, false, &applied, nil)
			hw.portraitSupported = func(uint16) bool { return tc.portraitSupported }
			path, err := programMonitorProfileOnFirstStartLocked(hw, tc.pool)
			if err == nil || path != "" || len(applied) != 0 {
				t.Fatalf("path %q, error %v, applied %v", path, err, applied)
			}
		})
	}
}

func TestProgramMonitorProfileOnFirstStartStopsAfterRecordedSuccess(t *testing.T) {
	monitorState(t, 1080, false, 1920)
	writes := 0
	hw := firstStartBackend(true, false, new([]string), nil)
	hw.apply = func(string) error { writes++; return os.WriteFile(monitorEDIDHashFile, []byte("recorded\n"), 0600) }
	for i := 0; i < 2; i++ {
		if _, err := programMonitorProfileOnFirstStartLocked(hw, poolFHD); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 {
		t.Fatalf("programmed %d times", writes)
	}
}

func TestCubeFirstStartChecksReceiverEDID(t *testing.T) {
	edid100 := testEDID(1920, 720, 1080, 45, 29700)
	edid60 := testEDID(1920, 280, 1080, 45, 14850, 16)
	for _, tc := range []struct {
		name    string
		saved   int
		edid    []byte
		readErr error
		path    string
		saved2  string
		checked bool
		wantErr bool
	}{
		{"1080p100 left in flash", 1080, edid100, nil, "NanoKVM-cube-monitor-1080.bin", "1080", true, false},
		{"720p saved", 720, edid100, nil, "NanoKVM-cube-monitor-720.bin", "720", true, false},
		{"QHD saved by a PCIe image", 1440, edid100, nil, "NanoKVM-cube-monitor-1080.bin", "0", true, false},
		{"Cube EDID", 1080, edid60, nil, "", "1080", true, false},
		{"read failure", 1080, nil, errors.New("i2c"), "", "1080", false, true},
		{"read returned zeros", 1080, make([]byte, 256), nil, "", "1080", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, tc.saved, false, 1920)
			var applied []string
			hw := firstStartBackend(true, true, &applied, nil)
			reads := 0
			hw.read = func() ([]byte, error) { reads++; return tc.edid, tc.readErr }
			path, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v", err)
			}
			if tc.path == "" {
				if path != "" || len(applied) != 0 {
					t.Fatalf("reported %q, applied %v", path, applied)
				}
			} else if filepath.Base(path) != tc.path || len(applied) != 1 || applied[0] != path {
				t.Fatalf("reported %q, applied %v", path, applied)
			}
			if saved, _ := os.ReadFile(monitorResolutionFile); string(saved) != tc.saved2 {
				t.Fatalf("saved resolution %q", saved)
			}
			if _, err := os.Stat(monitorEDIDCheckedFile); (err == nil) != tc.checked {
				t.Fatalf("checked marker present = %v", err == nil)
			}
			// A checked receiver is not read again on later starts.
			if tc.checked {
				if _, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD); err != nil || reads != 1 {
					t.Fatalf("second start: error %v, reads %d", err, reads)
				}
			}
		})
	}
}

// An image that identified a Cube as PCIe recorded the 100 Hz profile it
// programmed; the Cube is still checked, once.
func TestCubeFirstStartIgnoresProfileRecordedAsPCIe(t *testing.T) {
	monitorState(t, 1080, false, 1920)
	if err := os.WriteFile(monitorEDIDHashFile, []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var applied []string
	hw := firstStartBackend(true, true, &applied, nil)
	reads := 0
	hw.read = func() ([]byte, error) { reads++; return testEDID(1920, 720, 1080, 45, 29700), nil }
	path, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD)
	if err != nil || filepath.Base(path) != "NanoKVM-cube-monitor-1080.bin" || len(applied) != 1 {
		t.Fatalf("error %v, path %q, applied %v", err, path, applied)
	}
	if _, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD); err != nil || reads != 1 || len(applied) != 1 {
		t.Fatalf("second start: error %v, reads %d, applied %v", err, reads, applied)
	}
}
