package common

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

func readMonitorTestFile(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

func clearMonitorRestartHold(t *testing.T) {
	t.Cleanup(func() { monitorRestartPending = false })
}

func TestSaveMonitorSettingsForRestartDefersTheEDID(t *testing.T) {
	selectBoard(t, "pcie", "ux")
	monitorState(t, 1080, false, 1920)
	clearMonitorRestartHold(t)
	if err := os.WriteFile(monitorEDIDHashFile, []byte("recorded\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var applied []string
	height := uint16(1440)
	restore, err := saveMonitorSettingsForRestartLocked(MonitorSettings{Resolution: &height, SyncRefresh: true},
		poolQHD, firstStartBackend(true, false, &applied, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("programmed %v before the restart", applied)
	}
	if value, _ := readMonitorTestFile(t, monitorResolutionFile); value != "1440" {
		t.Fatalf("saved resolution %q", value)
	}
	// The first start after the restart programs the saved profile.
	if _, ok := readMonitorTestFile(t, monitorEDIDHashFile); ok {
		t.Fatal("the programmed-profile record was kept")
	}
	if err := applyMonitorPointerProfileLocked("/nonexistent", false); !errors.Is(err, ErrMonitorRestartPending) {
		t.Fatalf("EDID write during the pending restart: %v", err)
	}
	if path, err := programMonitorProfileOnFirstStartLocked(firstStartBackend(true, false, &applied, nil), poolQHD); err != nil ||
		path != monitorProfilePathAt(1440, GetScreen().FPS) {
		t.Fatalf("first start after the restart: %q, %v", path, err)
	}

	applied = nil
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if value, _ := readMonitorTestFile(t, monitorResolutionFile); value != "1080" {
		t.Fatalf("restored resolution %q", value)
	}
	if value, ok := readMonitorTestFile(t, monitorEDIDHashFile); !ok || value != "recorded\n" {
		t.Fatalf("restored record %q, %v", value, ok)
	}
	if monitorRestartPending {
		t.Fatal("the hold outlived the rollback")
	}
}

func TestSaveMonitorSettingsForRestartRestoresAbsentFiles(t *testing.T) {
	selectBoard(t, "pcie", "ux")
	monitorState(t, 1440, false, 1920)
	clearMonitorRestartHold(t)
	for _, path := range []string{monitorPortraitFile, monitorPortraitResolutionFile} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	on, size := true, uint16(2560)
	var applied []string
	restore, err := saveMonitorSettingsForRestartLocked(MonitorSettings{Portrait: &on, PortraitResolution: &size},
		poolQHD, firstStartBackend(true, false, &applied, nil))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := readMonitorTestFile(t, monitorPortraitFile); value != "1\n" {
		t.Fatalf("saved portrait %q", value)
	}
	if value, _ := readMonitorTestFile(t, monitorPortraitResolutionFile); value != "2560\n" {
		t.Fatalf("saved portrait profile %q", value)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{monitorPortraitFile, monitorPortraitResolutionFile, monitorEDIDHashFile, monitorDeferredFile} {
		if _, ok := readMonitorTestFile(t, path); ok {
			t.Fatalf("%s exists after the rollback", path)
		}
	}
	if value, _ := readMonitorTestFile(t, monitorResolutionFile); value != "1440" {
		t.Fatalf("resolution %q", value)
	}
}

func TestSaveMonitorSettingsForRestartRefuses(t *testing.T) {
	selectBoard(t, "pcie", "ux")
	uhd, qhd := uint16(2160), uint16(1440)
	for _, tc := range []struct {
		name       string
		height     *uint16
		pool       VideoPool
		powerCycle bool
	}{
		{"the target pool cannot capture it", &uhd, poolQHD, false},
		{"a limited receiver cannot capture it", &qhd, poolQHD, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, 1080, false, 1920)
			if tc.powerCycle {
				selectBoard(t, "beta", "c")
			}
			clearMonitorRestartHold(t)
			if err := os.WriteFile(monitorEDIDHashFile, []byte("recorded\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var applied []string
			if _, err := saveMonitorSettingsForRestartLocked(MonitorSettings{Resolution: tc.height}, tc.pool,
				firstStartBackend(true, tc.powerCycle, &applied, nil)); err == nil {
				t.Fatal("accepted")
			}
			if value, _ := readMonitorTestFile(t, monitorResolutionFile); value != "1080" || monitorRestartPending {
				t.Fatalf("changed state: resolution %q, hold %v", value, monitorRestartPending)
			}
			if _, ok := readMonitorTestFile(t, monitorEDIDHashFile); !ok {
				t.Fatal("removed the record")
			}
		})
	}
}

func TestMonitorStateFitsVideoPool(t *testing.T) {
	for _, tc := range []struct {
		state         MonitorState
		fhd, qhd, uhd bool
	}{
		{MonitorState{Resolution: 1080}, true, true, true},
		{MonitorState{Resolution: 0}, true, true, true},
		{MonitorState{Resolution: 1440}, false, true, true},
		{MonitorState{Resolution: 2160}, false, false, true},
		{MonitorState{Resolution: 1080, Portrait: true, PortraitResolution: 1920}, false, true, true},
		{MonitorState{Resolution: 1080, Portrait: true, PortraitResolution: 2560}, false, true, true},
		// The landscape resolution counts while portrait is on.
		{MonitorState{Resolution: 2160, Portrait: true, PortraitResolution: 1920}, false, false, true},
	} {
		got := [3]bool{tc.state.FitsVideoPool(poolFHD) == nil, tc.state.FitsVideoPool(poolQHD) == nil, tc.state.FitsVideoPool(poolUHD) == nil}
		if got != [3]bool{tc.fhd, tc.qhd, tc.uhd} {
			t.Errorf("%+v fits %v", tc.state, got)
		}
	}
}

func TestCubeUXCDeferredMonitorAfterMemorySwitch(t *testing.T) {
	for _, board := range []string{"alpha", "beta"} {
		for _, portrait := range []bool{false, true} {
			t.Run(board+fmt.Sprint(portrait), func(t *testing.T) {
				monitorState(t, 1080, false, 1920)
				selectBoard(t, board, "ux")
				clearMonitorRestartHold(t)
				var applied []string
				hw := firstStartBackend(true, true, &applied, nil)
				height := uint16(1440)
				update := MonitorSettings{Resolution: &height}
				if portrait {
					update = MonitorSettings{Portrait: &portrait}
				}
				restore, err := saveMonitorSettingsForRestartLocked(update, poolQHD, hw)
				if err != nil {
					t.Fatal(err)
				}
				if len(applied) != 0 {
					t.Fatal("EDID written before the new memory pool boots")
				}
				if value, ok := readMonitorTestFile(t, monitorDeferredFile); !ok || value != "1\n" {
					t.Fatal("missing deferred intent")
				}
				// Activation failure must restore the old state and remove the intent.
				if err := restore(); err != nil {
					t.Fatal(err)
				}
				if _, ok := readMonitorTestFile(t, monitorDeferredFile); ok {
					t.Fatal("rollback kept deferred intent")
				}
				if savedMonitorResolutionLocked() != 1080 || monitorPortraitEnabledLocked() {
					t.Fatal("rollback changed monitor")
				}
				if _, err := saveMonitorSettingsForRestartLocked(update, poolQHD, hw); err != nil {
					t.Fatal(err)
				}
				monitorRestartPending = false // fresh server process after reboot
				if _, err := programMonitorProfileOnFirstStartLocked(hw, poolFHD); err == nil || len(applied) != 0 {
					t.Fatal("wrote a profile that the booted memory pool cannot capture")
				}
				failing := hw
				failing.apply = func(string) error { return errors.New("flash failed") }
				if _, err := programMonitorProfileOnFirstStartLocked(failing, poolQHD); err == nil {
					t.Fatal("lost write failure")
				}
				if _, ok := readMonitorTestFile(t, monitorDeferredFile); !ok {
					t.Fatal("failed write consumed intent")
				}
				if _, err := programMonitorProfileOnFirstStartLocked(hw, poolQHD); err != nil {
					t.Fatal(err)
				}
				want := monitorProfilePathAt(1440, GetScreen().FPS)
				if portrait {
					want = portraitProfilePathAt(1920, GetScreen().FPS)
				}
				if len(applied) != 1 || applied[0] != want {
					t.Fatalf("deferred writes %v; want %s", applied, want)
				}
				if _, ok := readMonitorTestFile(t, monitorDeferredFile); ok {
					t.Fatal("successful write kept intent")
				}
				if _, err := programMonitorProfileOnFirstStartLocked(hw, poolQHD); err != nil || len(applied) != 1 {
					t.Fatal("ordinary Cube startup rewrote EDID")
				}
			})
		}
	}
}

func TestDeferredMonitorRejectsLimitedReceivers(t *testing.T) {
	for _, board := range []string{"alpha", "beta", "pcie"} {
		for _, chip := range []string{"c", "d"} {
			t.Run(board+"/"+chip, func(t *testing.T) {
				monitorState(t, 1080, false, 1920)
				selectBoard(t, board, chip)
				clearMonitorRestartHold(t)
				var applied []string
				hw := firstStartBackend(true, MonitorRequiresPowerCycle(), &applied, nil)
				hw.requireHardware = requireMonitorHardwareLocked
				height := uint16(1440)
				if _, err := saveMonitorSettingsForRestartLocked(MonitorSettings{Resolution: &height}, poolQHD, hw); err == nil {
					t.Fatal("accepted QHD on a limited receiver")
				}
				if _, ok := readMonitorTestFile(t, monitorDeferredFile); ok {
					t.Fatal("rejected request left deferred intent")
				}
				if savedMonitorResolutionLocked() != 1080 || monitorRestartPending {
					t.Fatal("rejected request changed state")
				}
			})
		}
	}
}
