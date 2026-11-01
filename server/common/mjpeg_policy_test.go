package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMjpegAllowedForFollowsTheInput(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		want          bool
	}{
		{"no signal", 0, 0, true},
		{"1080p", 1920, 1080, true},
		{"1440p", 2560, 1440, true},
		{"4K", 3840, 2160, false},
		{"portrait 1440x2560", 1440, 2560, true},
		{"portrait 4K", 2160, 3840, false},
	} {
		if got := MjpegAllowedFor(tc.width, tc.height); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// fakeCaptureFiles replaces the state files; nil values leave a file absent.
func fakeCaptureFiles(t *testing.T, width, height *string) {
	t.Helper()
	dir := t.TempDir()
	for name, value := range map[string]*string{"width": width, "height": height} {
		if value == nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(*value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	previous := nativeStateDir
	nativeStateDir = dir
	mjpegRefusal.Store(nil)
	t.Cleanup(func() { nativeStateDir = previous; mjpegRefusal.Store(nil) })
}

func str(s string) *string { return &s }

// The file check is a fast path only. With the files absent or stale the input
// may be 3840x2160 already; native capture, which checks the size it has
// detected under its own mutex, then refuses and the result must be the same
// code as the fast path gives.
func TestNativeRefusalIsTheBlockedResultWhateverTheFilesSay(t *testing.T) {
	nativeRefuses := func() ([]byte, int) { return nil, nativeMjpegBlocked }
	for _, tc := range []struct {
		name          string
		width, height *string
	}{
		{"files absent", nil, nil},
		{"width only", str("1920"), nil},
		{"stale 1080p", str("1920"), str("1080")},
		{"stale 1440p", str("2560"), str("1440")},
		{"4K", str("3840"), str("2160")},
	} {
		fakeCaptureFiles(t, tc.width, tc.height)
		data, result := readMjpegChecked(nativeRefuses)
		if data != nil || result != MjpegBlockedResult {
			t.Errorf("%s: data %v result %d, want blocked %d", tc.name, data, result, MjpegBlockedResult)
		}
	}
}

// A native refusal holds off native capture for the hold time; after it,
// native capture is asked again.
func TestNativeRefusalHoldsOffTheNextReads(t *testing.T) {
	fakeCaptureFiles(t, str("1920"), str("1080"))
	calls := 0
	refuse := func() ([]byte, int) { calls++; return nil, nativeMjpegBlocked }
	for i := 0; i < 3; i++ {
		if _, result := readMjpegChecked(refuse); result != MjpegBlockedResult {
			t.Fatalf("read %d: result %d", i, result)
		}
	}
	if calls != 1 {
		t.Fatalf("native capture called %d times within the hold, want 1", calls)
	}
	mjpegRefusal.Store(nil)
	if _, result := readMjpegChecked(func() ([]byte, int) { calls++; return []byte("jpeg"), 0 }); result != 0 || calls != 2 {
		t.Fatalf("after the hold: result %d, native calls %d", result, calls)
	}
}
