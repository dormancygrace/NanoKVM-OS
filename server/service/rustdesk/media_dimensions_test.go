package rustdesk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMediaDimensionBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "width")
	if got := readMediaDimension(path); got != 0 {
		t.Fatalf("missing dimension = %d", got)
	}
	for _, tc := range []struct {
		text string
		want uint16
	}{
		{"0", 0}, {"1", 1}, {" 2560\n", 2560}, {"65535", 65535},
		{"65536", 0}, {"-1", 0}, {"999999999999999999999999999", 0}, {"invalid", 0}, {"", 0},
	} {
		if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
			t.Fatal(err)
		}
		if got := readMediaDimension(path); got != tc.want {
			t.Errorf("%q: got %d, want %d", tc.text, got, tc.want)
		}
	}
}
