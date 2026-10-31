package common

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"
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
	mjpegRefusal.Store(nil)
	SetScreen("resolution", limit)
	t.Cleanup(func() {
		nativeStateDir = previousDir
		mjpegRefusal.Store(nil)
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

// The hold expires after elapsed time even when callers keep checking it.
// All readers share the same deadline; held reads must not refresh it.
func TestNativeRefusalHoldExpires(t *testing.T) {
	fakeCaptureFiles(t, nil, nil)
	synctest.Test(t, func(t *testing.T) {
		if MjpegRefusalHeld() {
			t.Fatal("held before any native refusal")
		}
		if _, result := readMjpegChecked(func() ([]byte, int) {
			return nil, nativeMjpegBlocked
		}); result != MjpegBlockedResult {
			t.Fatalf("initial refusal = %d", result)
		}

		var readers sync.WaitGroup
		for range 32 {
			readers.Go(func() {
				_, result := readMjpegChecked(func() ([]byte, int) {
					t.Error("native capture called inside the shared hold")
					return nil, 0
				})
				if result != MjpegBlockedResult {
					t.Errorf("held result = %d", result)
				}
			})
		}
		readers.Wait()
		time.Sleep(mjpegRefusalHold - time.Nanosecond)
		if !MjpegRefusalHeld() {
			t.Fatal("hold expired early")
		}
		time.Sleep(time.Nanosecond)
		if MjpegRefusalHeld() {
			t.Fatal("hold did not expire")
		}
		calls := 0
		data, result := readMjpegChecked(func() ([]byte, int) {
			calls++
			return []byte("jpeg"), 0
		})
		if calls != 1 || result != 0 || string(data) != "jpeg" {
			t.Fatalf("after expiry: calls %d result %d data %q", calls, result, data)
		}
	})
}

// The stream path reads no state files: with files saying 3840x2160 (or none)
// native capture is asked, and its answer counts.
func TestReadMjpegReadsNoStateFiles(t *testing.T) {
	for _, files := range [][2]*string{{str("3840"), str("2160")}, {nil, nil}} {
		fakeCaptureFiles(t, files[0], files[1])
		calls := 0
		if _, result := readMjpegChecked(func() ([]byte, int) { calls++; return []byte("jpeg"), 0 }); result != 0 || calls != 1 {
			t.Fatalf("result %d, native calls %d; want native capture asked", result, calls)
		}
	}
}

// The input became 3840x2160 while a capture was in flight: the refusal comes
// from native, and the next read stops at the hold without native capture.
func TestAllowedInputBecomesUHDBetweenTheCheckAndTheNativeCall(t *testing.T) {
	fakeCaptureFiles(t, str("1920"), str("1080"))
	nativeCalls := 0
	_, result := readMjpegChecked(func() ([]byte, int) {
		nativeCalls++
		// HDMI detection changes the input while the call is in flight.
		return nil, nativeMjpegBlocked
	})
	if nativeCalls != 1 || result != MjpegBlockedResult {
		t.Fatalf("native calls %d result %d, want 1 and %d", nativeCalls, result, MjpegBlockedResult)
	}
	_, result = readMjpegChecked(func() ([]byte, int) {
		nativeCalls++
		return nil, 0
	})
	if nativeCalls != 1 || result != MjpegBlockedResult {
		t.Fatalf("fast path let a call through: native calls %d result %d", nativeCalls, result)
	}
}

func TestNativeResultsOtherThanTheRefusalPassThrough(t *testing.T) {
	fakeCaptureFiles(t, str("1920"), str("1080"))
	for _, native := range []int{0, 5, -1, -2, -3, -4, -5, -6, -7} {
		data, result := readMjpegChecked(func() ([]byte, int) { return []byte("jpeg"), native })
		if result != native || string(data) != "jpeg" {
			t.Errorf("native %d became %d", native, result)
		}
	}
}

// The numbers cgo reads from the C header and the Go constants are one code.
func TestNativeRefusalCodeMatchesTheHeaders(t *testing.T) {
	for _, header := range []string{"../../support/additional/kvm/include/kvm_vision.h"} {
		text, err := os.ReadFile(header)
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile(`(?m)^#define\s+IMG_MJPEG_INPUT_BLOCKED\s+(-?\d+)\s*$`).FindSubmatch(text)
		if match == nil {
			t.Fatalf("%s: IMG_MJPEG_INPUT_BLOCKED is not defined", header)
		}
		code, _ := strconv.Atoi(string(match[1]))
		if code != nativeMjpegBlocked || normalizeNativeMjpegResult(code) != MjpegBlockedResult {
			t.Errorf("%s: IMG_MJPEG_INPUT_BLOCKED = %d, Go %d / %d", header, code, nativeMjpegBlocked, MjpegBlockedResult)
		}
	}
}
