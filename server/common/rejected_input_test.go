package common

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRejectedInput(t *testing.T) {
	previous := nativeStateDir
	t.Cleanup(func() { nativeStateDir = previous })
	for _, tc := range []struct {
		name          string
		width, height int
		write         bool
		mode          string
	}{
		{"none", 0, 0, false, ""},
		{"QHD", 2560, 1440, true, "qhd"},
		{"QHD portrait", 1440, 2560, true, "qhd"},
		{"UHD", 3840, 2160, true, "uhd"},
		{"UHD portrait", 2160, 3840, true, "uhd"},
		{"half written", 2560, 0, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nativeStateDir = t.TempDir()
			if tc.write {
				for name, value := range map[string]int{"rejected_width": tc.width, "rejected_height": tc.height} {
					if value == 0 {
						continue
					}
					if err := os.WriteFile(filepath.Join(nativeStateDir, name), []byte(strconv.Itoa(value)+"\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			width, height, mode := RejectedInput()
			if mode != tc.mode || (mode != "" && (width != tc.width || height != tc.height)) || (mode == "" && (width != 0 || height != 0)) {
				t.Fatalf("got %dx%d %q", width, height, mode)
			}
		})
	}
}
