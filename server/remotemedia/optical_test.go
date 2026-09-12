package remotemedia

import "testing"

func TestOpticalAddressBounds(t *testing.T) {
	for _, tt := range []struct {
		size       uint64
		dvd, valid bool
	}{
		{300*2048 - 2048, true, false}, {300 * 2048, true, true},
		{LegacyOpticalSize, false, true}, {LegacyOpticalSize + 2048, false, false},
		{LegacyOpticalSize + 2048, true, true}, {6 * 1024 * 1024 * 1024, true, true},
		{MaxOpticalSize, true, true}, {MaxOpticalSize + 2048, true, false},
		{MaxOpticalSize - 1, true, false},
	} {
		if got := ValidateOptical(tt.size, tt.dvd) == nil; got != tt.valid {
			t.Errorf("size=%d dvd=%v: valid=%v", tt.size, tt.dvd, got)
		}
	}
}
