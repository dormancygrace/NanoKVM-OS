package common

import "testing"

func TestEDIDHardwareCapabilities(t *testing.T) {
	for _, tc := range []struct {
		board, chip string
		want        bool
	}{
		{"alpha", "c", true}, {"beta", "c", true}, {"beta", "ux", true},
		{"beta", "d", true}, {"alpha", "ue", false}, {"beta", "ue", false},
		{"beta", "unknown", false}, {"unknown", "ux", false},
		{"pcie", "ux", true}, {"pcie", "c", false}, {"", "", false},
	} {
		if got := monitorHardwareSupported(tc.board, tc.chip); got != tc.want {
			t.Errorf("%s/%s supported=%v, want %v", tc.board, tc.chip, got, tc.want)
		}
	}
}
