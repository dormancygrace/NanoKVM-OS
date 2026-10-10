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

// fakeCapture reports a capture size and stream limit until the test ends.
func fakeCapture(t *testing.T, width, height string, limit int) {
	t.Helper()
	dir := t.TempDir()
	for name, value := range map[string]string{"width": width, "height": height} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	previousDir, previous := nativeStateDir, *GetScreen()
	nativeStateDir = dir
	SetScreen("resolution", limit)
	t.Cleanup(func() {
		nativeStateDir = previousDir
		SetScreen("resolution", int(previous.Height))
	})
}

func TestMjpegAllowedReadsTheCaptureNotTheLimit(t *testing.T) {
	fakeCapture(t, "3840", "2160", 0)
	for _, limit := range []int{0, 2160, 1440, 1080, 720} {
		SetScreen("resolution", limit)
		if MjpegAllowed() {
			t.Fatalf("MJPEG allowed at 3840x2160 with the stream limit %d", limit)
		}
	}
	if err := os.WriteFile(filepath.Join(nativeStateDir, "width"), []byte("1920"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nativeStateDir, "height"), []byte("1080"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !MjpegAllowed() {
		t.Fatal("a new capture size was not read")
	}
}

func TestReadMjpegRefusesWhileTheInputIs4K(t *testing.T) {
	fakeCapture(t, "3840", "2160", 1080)
	if _, result := GetKvmVision().ReadMjpeg(0, 0, 80); result != MjpegBlockedResult {
		t.Fatalf("result = %d, want %d", result, MjpegBlockedResult)
	}
	fakeCapture(t, "2560", "1440", 0)
	if _, result := GetKvmVision().ReadMjpeg(0, 0, 80); result == MjpegBlockedResult {
		t.Fatal("refused at 2560x1440")
	}
}
