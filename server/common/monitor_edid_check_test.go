package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEDID returns a valid 256-byte EDID: one base-block detailed timing and
// a CTA extension with the given short video descriptors.
func testEDID(h, hb, v, vb int, clock10kHz int, svds ...byte) []byte {
	e := make([]byte, 256)
	copy(e, []byte{0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0})
	for i := 38; i < 54; i++ {
		e[i] = 1 // unused standard timings
	}
	d := e[54:72]
	d[0], d[1] = byte(clock10kHz), byte(clock10kHz>>8)
	d[2], d[3], d[4] = byte(h), byte(hb), byte(h>>8<<4|hb>>8)
	d[5], d[6], d[7] = byte(v), byte(vb), byte(v>>8<<4|vb>>8)
	e[126] = 1
	x := e[128:]
	x[0], x[1] = 0x02, 0x03
	x[4] = 2<<5 | byte(len(svds))
	copy(x[5:], svds)
	x[2] = byte(5 + len(svds))
	for _, block := range [][]byte{e[:128], e[128:]} {
		var sum byte
		for _, b := range block[:127] {
			sum += b
		}
		block[127] = -sum
	}
	return e
}

func TestEDIDValid(t *testing.T) {
	good := testEDID(1920, 280, 1080, 45, 14850, 16)
	if !edidValid(good) {
		t.Fatal("valid EDID rejected")
	}
	ones := make([]byte, 256)
	for i := range ones {
		ones[i] = 0xff
	}
	bad := append([]byte(nil), good...)
	bad[200] ^= 1
	for name, edid := range map[string][]byte{"zeros": make([]byte, 256), "ones": ones, "extension checksum": bad, "short": good[:100]} {
		if edidValid(edid) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestEDIDValidRequiresAllDeclaredBlocks(t *testing.T) {
	good := testEDID(1920, 280, 1080, 45, 14850, 16)
	if edidValid(good[:128]) {
		t.Error("accepted missing declared extension")
	}
	if edidValid(good[:200]) {
		t.Error("accepted partial extension")
	}
	missing := append([]byte(nil), good...)
	missing[126]++
	missing[127]-- // Keep the base checksum valid, but declare two extensions.
	if edidValid(missing) {
		t.Error("accepted missing second extension")
	}
	complete := append(append([]byte(nil), missing...), make([]byte, 128)...)
	if !edidValid(complete) {
		t.Error("rejected complete EDID")
	}
	complete[300] = 1
	if edidValid(complete) {
		t.Error("accepted corrupt second extension")
	}
	baseOnly := append([]byte(nil), good[:128]...)
	baseOnly[126] = 0
	baseOnly[127]++
	if !edidValid(baseOnly) {
		t.Error("rejected base-only EDID")
	}
}

func TestEDIDBeyondCube(t *testing.T) {
	for _, tc := range []struct {
		name string
		edid []byte
		bad  bool
	}{
		{"1080p60", testEDID(1920, 280, 1080, 45, 14850, 16, 4), false},
		{"720p60", testEDID(1280, 370, 720, 30, 7425, 4), false},
		{"1080p100", testEDID(1920, 720, 1080, 45, 29700), true},
		{"1080p75", testEDID(1920, 280, 1080, 45, 18563), true},
		{"1440p30", testEDID(2560, 160, 1440, 41, 12050), true},
		{"portrait 1080x1920", testEDID(1080, 160, 1920, 41, 13750), true},
		{"1080p60 with VIC 64", testEDID(1920, 280, 1080, 45, 14850, 16, 64), true},
		{"1080p60 with VIC 97 (2160p60)", testEDID(1920, 280, 1080, 45, 14850, 97), true},
		{"1080p60 with native VIC 16", testEDID(1920, 280, 1080, 45, 14850, 0x80|16), false},
		{"1080p60 with native VIC 64", testEDID(1920, 280, 1080, 45, 14850, 0x80|64), true},
		{"short", make([]byte, 16), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode, bad := edidBeyondCube(tc.edid)
			if bad != tc.bad {
				t.Fatalf("beyond Cube = %v (%q), want %v", bad, mode, tc.bad)
			}
		})
	}
}

// The receiver's stock EDID must not be replaced.
func TestEDIDBeyondCubeStockProfile(t *testing.T) {
	edid, err := os.ReadFile("../../support/nanokvm_update_edid/E21_NanoKVM.bin")
	if err != nil {
		t.Fatal(err)
	}
	if mode, bad := edidBeyondCube(edid); bad {
		t.Fatalf("stock EDID flagged: %s", mode)
	}
}

// NANOKVM_EDID_PROFILES=<build>/tools/edid checks every generated profile:
// only the Cube, 600p, 720p and 1080p at 30/60 Hz profiles fit a Cube.
func TestEDIDBeyondCubeGeneratedProfiles(t *testing.T) {
	dir := os.Getenv("NANOKVM_EDID_PROFILES")
	if dir == "" {
		t.Skip("NANOKVM_EDID_PROFILES is not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*", "*.bin"))
	top, _ := filepath.Glob(filepath.Join(dir, "*.bin"))
	files = append(files, top...)
	if len(files) < 10 {
		t.Fatalf("only %d profiles in %s", len(files), dir)
	}
	safe := func(name string) bool {
		for _, prefix := range []string{"NanoKVM-cube-", "NanoKVM-monitor-600", "NanoKVM-monitor-720", "NanoKVM-stock", "E21_"} {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return name == "NanoKVM-monitor-1080-60.bin" || name == "NanoKVM-monitor-1080-30.bin"
	}
	for _, file := range files {
		edid, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(file)
		if mode, bad := edidBeyondCube(edid); bad == safe(name) {
			t.Errorf("%s: beyond Cube = %v (%s)", name, bad, mode)
		}
		// scripts/edid-rgb-range.py and its Go inverse agree on every profile.
		if old := profileWithoutRGBRange(edid); (old == nil) != (name == "NanoKVM-stock.bin") {
			t.Errorf("%s: RGB range block found = %v", name, old != nil)
		}
	}
}
