package common

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMonitorSettingsProgramsOnlyFinalProfile(t *testing.T) {
	height, portraitSize, off, on := uint16(1080), uint16(1280), false, true
	for _, tc := range []struct {
		name            string
		initialPortrait bool
		update          MonitorSettings
		want            string
		fail            bool
	}{
		{"leave portrait and change landscape", true, MonitorSettings{Resolution: &height, Portrait: &off}, "NanoKVM-monitor-1080-60.bin", false},
		{"enter portrait with new size and landscape preference", false, MonitorSettings{Resolution: &height, Portrait: &on, PortraitResolution: &portraitSize}, "NanoKVM-portrait-720x1280-60.bin", false},
		{"change active portrait size", true, MonitorSettings{PortraitResolution: &portraitSize}, "NanoKVM-portrait-720x1280-60.bin", false},
		{"save inactive portrait preference", false, MonitorSettings{PortraitResolution: &portraitSize}, "", false},
		{"inactive portrait preference and FPS", false, MonitorSettings{PortraitResolution: &portraitSize, SyncRefresh: true}, "NanoKVM-monitor-1440-60.bin", false},
		{"programming error preserves preferences", true, MonitorSettings{Resolution: &height, Portrait: &off}, "NanoKVM-monitor-1080-60.bin", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			oldRes, oldPortrait, oldPortraitRes := monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile
			oldDir, oldHash, oldFPS := monitorEDIDDir, monitorEDIDHashFile, GetScreen().FPS
			t.Cleanup(func() {
				monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile = oldRes, oldPortrait, oldPortraitRes
				monitorEDIDDir, monitorEDIDHashFile = oldDir, oldHash
				SetScreen("fps", oldFPS)
			})
			monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile = filepath.Join(dir, "resolution"), filepath.Join(dir, "portrait"), filepath.Join(dir, "portrait_resolution")
			monitorEDIDDir, monitorEDIDHashFile = dir, filepath.Join(dir, "hash")
			SetScreen("fps", 60)
			initial := "0"
			if tc.initialPortrait {
				initial = "1"
			}
			for name, value := range map[string]string{monitorResolutionFile: "1440", monitorPortraitFile: initial, monitorPortraitResolutionFile: "1920"} {
				if err := os.WriteFile(name, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"NanoKVM-monitor-1080-60.bin", "NanoKVM-monitor-1440-60.bin", "NanoKVM-portrait-720x1280-60.bin"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var paths []string
			hw := monitorSettingsBackend{
				requireHardware: func() error { return nil }, portraitSupported: func(uint16) bool { return true },
				powerCycle: func() bool { return false }, followsRefresh: func() bool { return true },
				apply: func(path string) error {
					paths = append(paths, filepath.Base(path))
					if tc.fail {
						return errors.New("EDID write failed")
					}
					return nil
				},
			}
			err := applyMonitorSettingsLocked(tc.update, hw)
			if (err != nil) != tc.fail {
				t.Fatalf("error = %v", err)
			}
			if tc.want == "" {
				if len(paths) != 0 {
					t.Fatalf("unexpected HDMI writes: %v", paths)
				}
			} else if len(paths) != 1 || paths[0] != tc.want {
				t.Fatalf("HDMI writes %v, want only %s", paths, tc.want)
			}
			wantHeight, wantPortrait, wantSize := uint16(1440), tc.initialPortrait, uint16(1920)
			if !tc.fail {
				if tc.update.Resolution != nil {
					wantHeight = *tc.update.Resolution
				}
				if tc.update.Portrait != nil {
					wantPortrait = *tc.update.Portrait
				}
				if tc.update.PortraitResolution != nil {
					wantSize = *tc.update.PortraitResolution
				}
			}
			if savedMonitorResolutionLocked() != wantHeight || monitorPortraitEnabledLocked() != wantPortrait || savedPortraitResolutionLocked() != wantSize {
				t.Fatal("saved monitor state differs from final request")
			}
		})
	}
}
