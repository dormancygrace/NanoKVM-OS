package common

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The pools of the five video memory modes (platform/build.sh).
var (
	poolFHD      = VideoPool{MiB: 56, Reusable: true}
	poolFHDFixed = VideoPool{MiB: 56}
	poolQHD      = VideoPool{MiB: 72, Reusable: true}
	poolQHDFixed = VideoPool{MiB: 72}
	poolUHD      = VideoPool{MiB: 128}
)

func TestVideoPoolGatesMatchTheModes(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		pool                  VideoPool
		qhd, portraitMax, uhd bool
	}{
		{"fhd", poolFHD, false, false, false},
		{"fhd-fixed", poolFHDFixed, false, false, false},
		{"qhd", poolQHD, true, true, false},
		{"qhd-fixed", poolQHDFixed, true, true, false},
		{"uhd", poolUHD, true, true, true},
		// There is no UHD image as CMA: it failed to get its buffers.
		{"uhd as CMA", VideoPool{MiB: 128, Reusable: true}, true, true, false},
		{"no node", VideoPool{}, false, false, false},
		{"just below QHD", VideoPool{MiB: 61}, false, false, false},
		{"between QHD and portrait", VideoPool{MiB: 63}, true, false, false},
	} {
		if tc.pool.QHD() != tc.qhd || tc.pool.PortraitMax() != tc.portraitMax || tc.pool.UHD() != tc.uhd {
			t.Errorf("%s: QHD %v portrait %v UHD %v", tc.name, tc.pool.QHD(), tc.pool.PortraitMax(), tc.pool.UHD())
		}
	}
}

func TestBootedVideoPoolReadsTheDeviceTree(t *testing.T) {
	dir := t.TempDir()
	old := ionNodeDir
	t.Cleanup(func() { ionNodeDir = old })
	ionNodeDir = dir
	if p := BootedVideoPool(); p != (VideoPool{}) {
		t.Fatalf("without a node: %+v", p)
	}
	if err := os.WriteFile(filepath.Join(dir, "size"), []byte{0x04, 0x80, 0x00, 0x00}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reusable"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if p := BootedVideoPool(); p != poolQHD {
		t.Fatalf("qhd: %+v", p)
	}
	if !SupportsQHD() || SupportsUHD() {
		t.Fatal("QHD pool: wrong capabilities")
	}
	if err := os.Remove(filepath.Join(dir, "reusable")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "size"), []byte{0x08, 0x00, 0x00, 0x00}, 0600); err != nil {
		t.Fatal(err)
	}
	if p := BootedVideoPool(); p != poolUHD || !SupportsUHD() {
		t.Fatalf("uhd: %+v", p)
	}
	if err := os.WriteFile(filepath.Join(dir, "size"), []byte{0x08, 0x00}, 0600); err != nil {
		t.Fatal(err)
	}
	if p := BootedVideoPool(); p != (VideoPool{}) {
		t.Fatalf("short property: %+v", p)
	}
}

// monitorState points the saved monitor files at a temporary directory.
func monitorState(t *testing.T, height int, portrait bool, portraitResolution int) {
	t.Helper()
	dir := t.TempDir()
	oldRes, oldPortrait, oldPortraitRes := monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile
	oldDir, oldHash := monitorEDIDDir, monitorEDIDHashFile
	t.Cleanup(func() {
		monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile = oldRes, oldPortrait, oldPortraitRes
		monitorEDIDDir, monitorEDIDHashFile = oldDir, oldHash
	})
	monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile = filepath.Join(dir, "resolution"), filepath.Join(dir, "portrait"), filepath.Join(dir, "portrait_resolution")
	monitorEDIDDir, monitorEDIDHashFile = dir, filepath.Join(dir, "hash")
	on := "0"
	if portrait {
		on = "1"
	}
	for name, value := range map[string]string{monitorResolutionFile: strconv.Itoa(height), monitorPortraitFile: on,
		monitorPortraitResolutionFile: strconv.Itoa(portraitResolution)} {
		if err := os.WriteFile(name, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMonitorFitsVideoPool(t *testing.T) {
	for _, tc := range []struct {
		name               string
		height             int
		portrait           bool
		portraitResolution int
		fits               map[string]bool
	}{
		{"auto", 0, false, 1920, map[string]bool{"fhd": true, "fhd-fixed": true, "qhd": true, "qhd-fixed": true, "uhd": true}},
		{"1080p", 1080, false, 1920, map[string]bool{"fhd": true, "fhd-fixed": true, "qhd": true, "qhd-fixed": true, "uhd": true}},
		{"1440p", 1440, false, 1920, map[string]bool{"fhd": false, "fhd-fixed": false, "qhd": true, "qhd-fixed": true, "uhd": true}},
		{"2160p", 2160, false, 1920, map[string]bool{"fhd": false, "fhd-fixed": false, "qhd": false, "qhd-fixed": false, "uhd": true}},
		{"portrait 1080x1920", 1080, true, 1920, map[string]bool{"fhd": false, "fhd-fixed": false, "qhd": true, "qhd-fixed": true, "uhd": true}},
		{"portrait 1440x2560", 1080, true, 2560, map[string]bool{"fhd": false, "fhd-fixed": false, "qhd": true, "qhd-fixed": true, "uhd": true}},
		// Turning portrait off restores the landscape profile.
		{"portrait over 2160p", 2160, true, 1920, map[string]bool{"fhd": false, "fhd-fixed": false, "qhd": false, "qhd-fixed": false, "uhd": true}},
		// An inactive portrait choice advertises nothing.
		{"saved portrait size, portrait off", 1080, false, 2560, map[string]bool{"fhd": true, "fhd-fixed": true, "qhd": true, "qhd-fixed": true, "uhd": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, tc.height, tc.portrait, tc.portraitResolution)
			pools := map[string]VideoPool{"fhd": poolFHD, "fhd-fixed": poolFHDFixed, "qhd": poolQHD, "qhd-fixed": poolQHDFixed, "uhd": poolUHD}
			for mode, want := range tc.fits {
				err := MonitorFitsVideoPool(pools[mode])
				if (err == nil) != want {
					t.Errorf("%s: error %v, want fit %v", mode, err, want)
				}
				if err != nil && !strings.Contains(err.Error(), "video memory mode") {
					t.Errorf("%s: unhelpful error %q", mode, err)
				}
			}
		})
	}
}

func TestFitMonitorToVideoPool(t *testing.T) {
	for _, tc := range []struct {
		name               string
		pool               VideoPool
		height             int
		portrait           bool
		portraitResolution int
		wantHeight         int
		wantPortrait       bool
		writes             int
		hardware           bool
	}{
		{"fits", poolQHD, 1440, false, 1920, 1440, false, 0, true},
		{"uhd stays", poolUHD, 2160, true, 2560, 2160, true, 0, true},
		{"1440p under FHD", poolFHD, 1440, false, 1920, 1080, false, 1, true},
		{"1440p under fixed FHD", poolFHDFixed, 1440, false, 1920, 1080, false, 1, true},
		{"2160p under QHD", poolQHD, 2160, false, 1920, 1440, false, 1, true},
		{"2160p under QHD with portrait", poolQHD, 2160, true, 1920, 1440, true, 1, true},
		{"2160p under FHD", poolFHD, 2160, false, 1920, 1080, false, 1, true},
		{"2160p as 128 MiB CMA", VideoPool{MiB: 128, Reusable: true}, 2160, false, 1920, 1440, false, 1, true},
		{"portrait under FHD", poolFHD, 1080, true, 1920, 1080, false, 1, true},
		{"portrait and 1440p under FHD in one write", poolFHD, 1440, true, 2560, 1080, false, 1, true},
		{"maximum portrait below 64 MiB", VideoPool{MiB: 63}, 1080, true, 2560, 1080, false, 1, true},
		{"no programmable EDID", poolFHD, 1440, true, 1920, 1440, true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, tc.height, tc.portrait, tc.portraitResolution)
			writes := 0
			hw := monitorSettingsBackend{
				requireHardware: func() error {
					if !tc.hardware {
						return errors.New("EDID programming is unsupported")
					}
					return nil
				},
				portraitSupported: func(resolution uint16) bool {
					return tc.pool.QHD() && (resolution != portraitResolutionMax || tc.pool.PortraitMax())
				},
				powerCycle: func() bool { return false }, followsRefresh: func() bool { return false },
				apply: func(string) error { writes++; return nil },
			}
			changed, err := fitMonitorToVideoPoolLocked(tc.pool, hw)
			if err != nil || changed != (tc.writes > 0) || writes != tc.writes {
				t.Fatalf("changed %v, error %v, %d writes", changed, err, writes)
			}
			if int(savedMonitorResolutionLocked()) != tc.wantHeight || monitorPortraitEnabledLocked() != tc.wantPortrait {
				t.Fatalf("saved %d portrait %v, want %d %v", savedMonitorResolutionLocked(), monitorPortraitEnabledLocked(), tc.wantHeight, tc.wantPortrait)
			}
			if tc.hardware && monitorFitsVideoPoolLocked(tc.pool) != nil {
				t.Fatalf("the fitted profile does not fit: %v", monitorFitsVideoPoolLocked(tc.pool))
			}
		})
	}
}

func TestFitMonitorKeepsTheSavedProfileWhenProgrammingFails(t *testing.T) {
	monitorState(t, 1440, false, 1920)
	hw := monitorSettingsBackend{
		requireHardware: func() error { return nil }, portraitSupported: func(uint16) bool { return true },
		powerCycle: func() bool { return false }, followsRefresh: func() bool { return false },
		apply: func(string) error { return errors.New("EDID write failed") },
	}
	if changed, err := fitMonitorToVideoPoolLocked(poolFHD, hw); changed || err == nil {
		t.Fatalf("changed %v, error %v", changed, err)
	}
	if savedMonitorResolutionLocked() != 1440 {
		t.Fatal("the saved resolution changed although the EDID was not programmed")
	}
}
