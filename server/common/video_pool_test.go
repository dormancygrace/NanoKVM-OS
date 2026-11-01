package common

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The pools of the three video memory modes (platform/build.sh).
var (
	poolFHD = VideoPool{MiB: 40}
	poolQHD = VideoPool{MiB: 54}
	poolUHD = VideoPool{MiB: 104}
)

func TestVideoPoolGatesMatchTheModes(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		pool                  VideoPool
		qhd, portraitMax, uhd bool
	}{
		{"fhd", poolFHD, false, false, false},
		{"qhd", poolQHD, true, true, false},
		{"uhd", poolUHD, true, true, true},
		// The CMA images of earlier releases, sized to the peak: UHD as CMA
		// failed to get its buffers.
		{"qhd as CMA", VideoPool{MiB: 56, Reusable: true}, true, true, false},
		{"uhd as CMA", VideoPool{MiB: 128, Reusable: true}, true, true, false},
		// The shared 144 MiB region keeps enough slack for 2160p.
		{"shared CMA", VideoPool{MiB: 144, Reusable: true}, true, true, true},
		{"just below shared CMA", VideoPool{MiB: 143, Reusable: true}, true, true, false},
		{"no node", VideoPool{}, false, false, false},
		{"just below QHD", VideoPool{MiB: 53}, false, false, false},
		{"just below UHD", VideoPool{MiB: 103}, true, true, false},
	} {
		if tc.pool.QHD() != tc.qhd || tc.pool.PortraitMax() != tc.portraitMax || tc.pool.UHD() != tc.uhd {
			t.Errorf("%s: QHD %v portrait %v UHD %v", tc.name, tc.pool.QHD(), tc.pool.PortraitMax(), tc.pool.UHD())
		}
	}
}

// monitorState points the saved monitor files at a temporary directory.
func monitorState(t *testing.T, height int, portrait bool, portraitResolution int) {
	t.Helper()
	dir := t.TempDir()
	oldRes, oldPortrait, oldPortraitRes := monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile
	oldDir, oldHash, oldChecked := monitorEDIDDir, monitorEDIDHashFile, monitorEDIDCheckedFile
	oldDeferred, oldReceiver := monitorDeferredFile, monitorReceiverCheckedFile
	oldVerified, oldVersion := monitorReceiverVerifiedFile, monitorSystemVersionFile
	t.Cleanup(func() {
		monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile = oldRes, oldPortrait, oldPortraitRes
		monitorEDIDDir, monitorEDIDHashFile, monitorEDIDCheckedFile = oldDir, oldHash, oldChecked
		monitorDeferredFile, monitorReceiverCheckedFile = oldDeferred, oldReceiver
		monitorReceiverVerifiedFile, monitorSystemVersionFile = oldVerified, oldVersion
	})
	monitorReceiverCheckedFile = filepath.Join(dir, "receiver_checked")
	monitorReceiverVerifiedFile, monitorSystemVersionFile = filepath.Join(dir, "receiver_verified"), filepath.Join(dir, "version")
	monitorResolutionFile, monitorPortraitFile, monitorPortraitResolutionFile = filepath.Join(dir, "resolution"), filepath.Join(dir, "portrait"), filepath.Join(dir, "portrait_resolution")
	monitorEDIDDir, monitorEDIDHashFile, monitorEDIDCheckedFile = dir, filepath.Join(dir, "hash"), filepath.Join(dir, "checked")
	monitorDeferredFile = filepath.Join(dir, "deferred")
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
		{"auto", 0, false, 1920, map[string]bool{"fhd": true, "qhd": true, "uhd": true}},
		{"1080p", 1080, false, 1920, map[string]bool{"fhd": true, "qhd": true, "uhd": true}},
		{"1440p", 1440, false, 1920, map[string]bool{"fhd": false, "qhd": true, "uhd": true}},
		{"2160p", 2160, false, 1920, map[string]bool{"fhd": false, "qhd": false, "uhd": true}},
		{"portrait 1080x1920", 1080, true, 1920, map[string]bool{"fhd": false, "qhd": true, "uhd": true}},
		{"portrait 1440x2560", 1080, true, 2560, map[string]bool{"fhd": false, "qhd": true, "uhd": true}},
		// Turning portrait off restores the landscape profile.
		{"portrait over 2160p", 2160, true, 1920, map[string]bool{"fhd": false, "qhd": false, "uhd": true}},
		// An inactive portrait choice advertises nothing.
		{"saved portrait size, portrait off", 1080, false, 2560, map[string]bool{"fhd": true, "qhd": true, "uhd": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, tc.height, tc.portrait, tc.portraitResolution)
			pools := map[string]VideoPool{"fhd": poolFHD, "qhd": poolQHD, "uhd": poolUHD}
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
	selectBoard(t, "pcie", "ux")
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
		{"2160p under QHD", poolQHD, 2160, false, 1920, 1440, false, 1, true},
		{"2160p under QHD with portrait", poolQHD, 2160, true, 1920, 1440, true, 1, true},
		{"2160p under FHD", poolFHD, 2160, false, 1920, 1080, false, 1, true},
		{"2160p as 128 MiB CMA", VideoPool{MiB: 128, Reusable: true}, 2160, false, 1920, 1440, false, 1, true},
		{"portrait under FHD", poolFHD, 1080, true, 1920, 1080, false, 1, true},
		{"portrait and 1440p under FHD in one write", poolFHD, 1440, true, 2560, 1080, false, 1, true},
		{"maximum portrait below 54 MiB", VideoPool{MiB: 53}, 1080, true, 2560, 1080, false, 1, true},
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
