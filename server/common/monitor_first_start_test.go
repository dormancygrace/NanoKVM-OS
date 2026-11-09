package common

import (
	"crypto/sha256"
	"encoding/hex"
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
		apply: func(path string, _ *MonitorIdentity) error {
			*applied = append(*applied, path)
			return fail
		},
		read:        func() ([]byte, error) { return heldEDID(), nil },
		readAtStart: true,
	}
}

// heldEDID is what the receiver holds in firstStartBackend.
func heldEDID() []byte { return testEDID(1920, 280, 1080, 45, 14850, 16) }

func edidHash(edid []byte) string {
	sum := sha256.Sum256(edid)
	return hex.EncodeToString(sum[:])
}

// recordHeld records the profile the receiver holds, as after a write.
func recordHeld(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(monitorEDIDHashFile, []byte(edidHash(heldEDID())+"\n"), 0600); err != nil {
		t.Fatal(err)
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
				recordHeld(t)
			}
			// The Cube's own EDID checks are TestCubeFirstStartChecksReceiverEDID.
			if err := errors.Join(markMonitorEDIDCheckedLocked(), markMonitorRGBRangeCheckedLocked()); err != nil {
				t.Fatal(err)
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

// With a profile recorded but not yet verified, startup reads the receiver. The saved profile is
// programmed again only when the receiver holds another EDID, and an unreadable
// receiver is left alone.
func TestFirstStartChecksTheReceiver(t *testing.T) {
	other := testEDID(1920, 160, 1080, 53, 23550, 16)
	oneBlock := testEDID(1280, 370, 720, 30, 7425)[:128]
	oneBlock[126], oneBlock[127] = 0, oneBlock[127]+1
	padded := append(append([]byte(nil), oneBlock...), make([]byte, 256-len(oneBlock))...)
	for _, tc := range []struct {
		name     string
		chip     string
		recorded []byte
		held     []byte
		readErr  error
		reads    int
		writes   int
	}{
		{"holds the recorded profile", "ux", heldEDID(), heldEDID(), nil, 1, 0},
		{"holds another EDID", "ux", heldEDID(), other, nil, 1, 1},
		{"one-block profile in a 256-byte copy", "ux", oneBlock, padded, nil, 1, 0},
		{"read failure", "ux", heldEDID(), nil, errors.New("i2c"), 1, 0},
		{"read returned zeros", "ux", heldEDID(), make([]byte, 256), nil, 1, 0},
		{"LT6911D holds another EDID", "d", heldEDID(), other, nil, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, 1080, false, 1920)
			selectBoard(t, "pcie", tc.chip)
			if err := os.WriteFile(monitorEDIDHashFile, []byte(edidHash(tc.recorded)+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var applied []string
			reads := 0
			hw := firstStartBackend(true, false, &applied, nil)
			hw.read = func() ([]byte, error) { reads++; return tc.held, tc.readErr }
			path, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD)
			if err != nil || reads != tc.reads || len(applied) != tc.writes {
				t.Fatalf("error %v, reads %d, applied %v", err, reads, applied)
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

// An LT6911C is reprogrammed when its EDID advertises modes it cannot capture,
// or does not declare the RGB range selectable, as the factory EDID: the
// source then sends limited-range RGB and the picture is washed out.
func TestCubeFirstStartChecksReceiverEDID(t *testing.T) {
	edid100 := testEDID(1920, 720, 1080, 45, 29700)
	edid60 := testEDID(1920, 280, 1080, 45, 14850, 16)
	edid60Range := withRGBRange(t, edid60)
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
		{"factory EDID without the RGB range", 1080, edid60, nil, "NanoKVM-cube-monitor-1080.bin", "1080", true, false},
		{"QHD saved, EDID without the RGB range", 1440, edid60, nil, "NanoKVM-cube-monitor-1080.bin", "0", true, false},
		{"Cube EDID with the RGB range", 1080, edid60Range, nil, "", "1080", true, false},
		{"read failure", 1080, nil, errors.New("i2c"), "", "1080", false, true},
		{"read returned zeros", 1080, make([]byte, 256), nil, "", "1080", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, tc.saved, false, 1920)
			selectBoard(t, "beta", "c")
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
			if _, err := os.Stat(monitorRGBRangeCheckedFile); (err == nil) != tc.checked {
				t.Fatalf("RGB range marker present = %v", err == nil)
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

// Every Cube or Lite programs a profile over a stock EDID, which declares no
// RGB range: an LT6911D at once, an LT6911C or LT6911UXC for the next power
// cycle.
func TestCubeFirstStartProgramsProfileOverStockEDID(t *testing.T) {
	for _, tc := range []struct {
		name, board, chip string
		want              bool
		powerCycle        bool
	}{
		{"beta d", "beta", "d", true, false},
		{"alpha d", "alpha", "d", true, false},
		{"beta c", "beta", "c", true, true},
		{"beta ux", "beta", "ux", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, 0, false, 1920)
			selectBoard(t, tc.board, tc.chip)
			if MonitorRequiresPowerCycle() != tc.powerCycle {
				t.Fatalf("power cycle = %v", MonitorRequiresPowerCycle())
			}
			var applied []string
			hw := firstStartBackend(true, MonitorRequiresPowerCycle(), &applied, nil)
			path, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD)
			if err != nil || (len(applied) == 1) != tc.want {
				t.Fatalf("programmed %q (%v), applied %v; want a write: %v", path, err, applied, tc.want)
			}
		})
	}
}

// A Cube or Lite with an LT6911UXC takes every profile: only an EDID without
// the RGB range declaration makes its first start program the saved one, once.
func TestCubeLT6911UXCFirstStartChecksRGBRange(t *testing.T) {
	edid := testEDID(1920, 280, 1080, 45, 14850, 16)
	for _, tc := range []struct {
		name string
		held []byte
		want bool
	}{
		{"without the RGB range", edid, true},
		{"with the RGB range", withRGBRange(t, edid), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, 1080, false, 1920)
			selectBoard(t, "beta", "ux")
			var applied []string
			reads := 0
			hw := firstStartBackend(true, true, &applied, nil)
			hw.read = func() ([]byte, error) { reads++; return tc.held, nil }
			for start := 0; start < 2; start++ {
				if _, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD); err != nil {
					t.Fatal(err)
				}
			}
			if reads != 1 || (len(applied) == 1) != tc.want || len(applied) > 1 {
				t.Fatalf("reads %d, applied %v; want a write: %v", reads, applied, tc.want)
			}
			if tc.want && applied[0] != monitorProfilePathAt(1080, GetScreen().FPS) {
				t.Fatalf("applied %v", applied)
			}
		})
	}
	// With HDMI capture disabled the receiver is not read, and the check waits.
	t.Run("HDMI disabled", func(t *testing.T) {
		monitorState(t, 1080, false, 1920)
		selectBoard(t, "beta", "ux")
		var applied []string
		reads := 0
		hw := firstStartBackend(true, true, &applied, nil)
		hw.readAtStart = false
		hw.read = func() ([]byte, error) { reads++; return edid, nil }
		if _, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD); err != nil || reads != 0 || len(applied) != 0 {
			t.Fatalf("error %v, reads %d, applied %v", err, reads, applied)
		}
		if _, err := os.Stat(monitorRGBRangeCheckedFile); err == nil {
			t.Fatal("marked as checked")
		}
	})
}

// An LT6911D write on a Cube or Lite whose replug did not complete leaves the
// profile in flash, recorded and readable as held: the next start applies it
// again so the EDID tool can finish the replug, unless HDMI is disabled.
func TestPendingReplugAppliesTheHeldProfileAgain(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pending     bool
		readAtStart bool
		writes      int
	}{
		{"pending", true, true, 1},
		{"no pending replug", false, true, 0},
		{"HDMI disabled", true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, 1080, false, 1920)
			selectBoard(t, "beta", "d")
			if err := os.WriteFile(monitorEDIDHashFile, []byte(edidHash(heldEDID())+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.pending {
				if err := os.WriteFile(monitorReplugPendingFile, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var applied []string
			hw := firstStartBackend(true, false, &applied, nil)
			hw.readAtStart = tc.readAtStart
			hw.read = func() ([]byte, error) { return heldEDID(), nil }
			if _, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD); err != nil || len(applied) != tc.writes {
				t.Fatalf("error %v, applied %v; want %d writes", err, applied, tc.writes)
			}
		})
	}
}
