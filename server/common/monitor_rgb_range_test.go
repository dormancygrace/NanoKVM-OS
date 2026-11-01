package common

import (
	"bytes"
	"os"
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

func TestProfileWithoutRGBRange(t *testing.T) {
	stock := stockEDID(t)
	if profileWithoutRGBRange(stock) != nil {
		t.Fatal("stock profile has no RGB range block")
	}
	if old := profileWithoutRGBRange(withRGBRange(t, stock)); !bytes.Equal(old, stock) {
		t.Fatalf("earlier version differs:\n%x\n%x", old, stock)
	}
}
