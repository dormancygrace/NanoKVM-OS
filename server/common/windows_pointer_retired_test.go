package common

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A device updated with the Windows pointer on: the recorded profile is
// programmed as shipped, once, and the marker and ContainerID are removed;
// where the profile never carried the block they are only removed.
func TestRetireWindowsPointer(t *testing.T) {
	profile := []byte("profile")
	sum := sha256.Sum256(profile)
	for _, tc := range []struct {
		name     string
		marker   bool
		follows  bool
		recorded string
		fail     error
		writes   int
		removed  bool
	}{
		{"pointer on", true, true, hex.EncodeToString(sum[:]), nil, 1, true},
		{"pointer off", false, true, hex.EncodeToString(sum[:]), nil, 0, false},
		{"recorded profile not shipped", true, true, "0", nil, 0, true},
		{"EDID does not follow the stream", true, false, hex.EncodeToString(sum[:]), nil, 0, true},
		{"write fails", true, true, hex.EncodeToString(sum[:]), errors.New("busy"), 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, 1080, false, 1920)
			oldMarker, oldID := retiredWindowsPointerMarker, retiredContainerIDFile
			t.Cleanup(func() { retiredWindowsPointerMarker, retiredContainerIDFile = oldMarker, oldID })
			retiredWindowsPointerMarker = filepath.Join(monitorEDIDDir, "usb.pointer_windows")
			retiredContainerIDFile = filepath.Join(monitorEDIDDir, "usb_container_id")
			path := filepath.Join(monitorEDIDDir, "NanoKVM-monitor-1080-60.bin")
			files := map[string]string{path: string(profile), monitorEDIDHashFile: tc.recorded + "\n", retiredContainerIDFile: "id\n"}
			if tc.marker {
				files[retiredWindowsPointerMarker] = ""
			}
			for name, data := range files {
				if err := os.WriteFile(name, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var applied []string
			hw := firstStartBackend(true, false, &applied, tc.fail)
			hw.followsRefresh = func() bool { return tc.follows }
			got, err := retireWindowsPointerLocked(hw)
			if (err != nil) != (tc.fail != nil) || len(applied) != tc.writes {
				t.Fatalf("error %v, applied %v", err, applied)
			}
			if tc.writes == 1 && (applied[0] != path || (tc.fail == nil && got != path)) {
				t.Fatalf("applied %v, reported %q", applied, got)
			}
			_, err = os.Stat(retiredWindowsPointerMarker)
			markerGone := errors.Is(err, os.ErrNotExist)
			_, err = os.Stat(retiredContainerIDFile)
			idGone := errors.Is(err, os.ErrNotExist)
			if idGone != tc.removed || markerGone != (tc.removed || !tc.marker) {
				t.Fatalf("marker gone %v, ContainerID gone %v", markerGone, idGone)
			}
		})
	}
}
