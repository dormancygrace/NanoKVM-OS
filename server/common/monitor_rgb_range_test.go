package common

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// withRGBRange appends the block as scripts/edid-rgb-range.py does.
func withRGBRange(t *testing.T, edid []byte) []byte {
	t.Helper()
	out := append([]byte(nil), edid...)
	ext := out[128:]
	end := int(ext[2])
	copy(ext[end+3:127], edid[128+end:128+124])
	copy(ext[end:end+3], []byte{0xE2, 0x00, rgbRangeCapability})
	ext[2] = byte(end + 3)
	var sum byte
	for _, b := range ext[:127] {
		sum += b
	}
	ext[127] = -sum
	if !edidValid(out) {
		t.Fatal("invalid test profile")
	}
	return out
}

func stockEDID(t *testing.T) []byte {
	t.Helper()
	edid, err := os.ReadFile("../../support/nanokvm_update_edid/E21_NanoKVM.bin")
	if err != nil {
		t.Fatal(err)
	}
	return edid
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// A device updated with the Windows pointer on and a profile written before
// the RGB range block: one write, of the current version.
func TestRetiredPointerAndRGBRangeWriteOnce(t *testing.T) {
	stock := stockEDID(t)
	monitorState(t, 1080, false, 1920)
	oldMarker, oldID := retiredWindowsPointerMarker, retiredContainerIDFile
	t.Cleanup(func() { retiredWindowsPointerMarker, retiredContainerIDFile = oldMarker, oldID })
	retiredWindowsPointerMarker = filepath.Join(monitorEDIDDir, "usb.pointer_windows")
	retiredContainerIDFile = filepath.Join(monitorEDIDDir, "usb_container_id")
	path := filepath.Join(monitorEDIDDir, "NanoKVM-monitor-1080-60.bin")
	for name, data := range map[string][]byte{path: withRGBRange(t, stock), monitorEDIDHashFile: []byte(hashOf(stock) + "\n"),
		retiredWindowsPointerMarker: nil, retiredContainerIDFile: []byte("id\n"), filepath.Join(monitorEDIDDir, "checked"): nil} {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var applied []string
	hw := firstStartBackend(true, false, &applied, nil)
	hw.followsRefresh = func() bool { return true }
	// the backend records as applyMonitorProfileLocked does
	apply := hw.apply
	hw.apply = func(p string) error { recordMonitorProfileLocked(p); return apply(p) }
	// the receiver holds what was last written
	hw.read = func() ([]byte, error) {
		if len(applied) == 0 {
			return stock, nil
		}
		return os.ReadFile(applied[len(applied)-1])
	}
	if _, err := retireWindowsPointerLocked(hw); err != nil {
		t.Fatal(err)
	}
	if _, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0] != path {
		t.Fatalf("programmed %v, want once %s", applied, path)
	}
}

func TestProfileWithoutRGBRange(t *testing.T) {
	stock := stockEDID(t)
	if profileWithoutRGBRange(stock) != nil {
		t.Fatal("stock profile has no RGB range block")
	}
	if old := profileWithoutRGBRange(withRGBRange(t, stock)); !bytes.Equal(old, stock) {
		t.Fatalf("earlier version differs:\n%x\n%x", old, stock)
	}
}

// The first start after the update programs the current version of the
// profile the receiver holds, whatever the saved settings would select; other
// recorded profiles are left alone.
func TestFirstStartAddsRGBRangeToRecordedProfile(t *testing.T) {
	stock := stockEDID(t)
	current := withRGBRange(t, stock)
	for _, tc := range []struct {
		name       string
		held       []byte // the receiver holds the recorded profile
		stock      bool   // the stock profile is shipped as it is
		powerCycle bool
		want       string
	}{
		{"earlier version of a shipped profile", stock, false, false, "NanoKVM-monitor-1080-60.bin"},
		{"current version", current, false, false, ""},
		{"a shipped profile without the block", stock, true, false, ""},
		{"unknown profile", heldEDID(), false, false, ""},
		{"Cube", stock, false, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitorState(t, 1080, false, 1920)
			shipped := map[string][]byte{"NanoKVM-monitor-1080-60.bin": current}
			if tc.stock {
				shipped["NanoKVM-stock.bin"] = stock
			}
			for name, data := range shipped {
				if err := os.WriteFile(filepath.Join(monitorEDIDDir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(monitorEDIDDir, "checked"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(monitorEDIDHashFile, []byte(hashOf(tc.held)+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var applied []string
			hw := firstStartBackend(true, tc.powerCycle, &applied, nil)
			hw.read = func() ([]byte, error) { return tc.held, nil }
			path, err := programMonitorProfileOnFirstStartLocked(hw, poolUHD)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(applied) != 0 || path != "" {
					t.Fatalf("programmed %v (%q)", applied, path)
				}
				return
			}
			want := filepath.Join(monitorEDIDDir, tc.want)
			if len(applied) != 1 || applied[0] != want || path != want {
				t.Fatalf("programmed %v (%q), want %s", applied, path, want)
			}
		})
	}
}
