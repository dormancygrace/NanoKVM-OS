package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectedInput(t *testing.T) {
	previous := nativeStateDir
	t.Cleanup(func() { nativeStateDir = previous })
	for _, tc := range []struct {
		name, record  string
		width, height int
	}{
		{"none", "", 0, 0},
		{"QHD", "2560 1440\n", 2560, 1440},
		{"QHD portrait", "1440 2560\n", 1440, 2560},
		{"UHD", "3840 2160\n", 3840, 2160},
		{"UHD portrait", "2160 3840\n", 2160, 3840},
		{"partial", "2560", 0, 0},
		{"extra", "2560 1440 1", 0, 0},
		{"zero", "2560 0", 0, 0},
		{"negative", "-2560 1440", 0, 0},
		{"overflow", "65536 1440", 0, 0},
		{"malformed", "2560x1440", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nativeStateDir = t.TempDir()
			if tc.record != "" {
				if err := os.WriteFile(filepath.Join(nativeStateDir, "rejected_input"), []byte(tc.record), 0644); err != nil {
					t.Fatal(err)
				}
			}
			width, height := RejectedInput()
			if width != tc.width || height != tc.height {
				t.Fatalf("got %dx%d, want %dx%d", width, height, tc.width, tc.height)
			}
		})
	}
}
