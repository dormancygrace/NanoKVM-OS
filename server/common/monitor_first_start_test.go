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
		apply: func(path string) error {
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
// receiver is left alone. The LT6911D on PCIe is not read.
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
		{"LT6911D", "d", heldEDID(), other, nil, 0, 0},
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
