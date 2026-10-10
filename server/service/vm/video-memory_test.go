package vm

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const dtBase = "sys/firmware/devicetree/base/"

func videoMemoryRoot(t *testing.T) (root string, put func(path, value string)) {
	t.Helper()
	root = t.TempDir()
	put = func(path, value string) {
		t.Helper()
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put(dtBase+"sipeed,board-revision", "pcie\x00")
	return root, put
}

func putImages(put func(path, value string), names ...string) {
	for _, name := range names {
		put("usr/lib/nanokvm/boot/"+name+".dtb", "fit")
	}
}

func TestVideoMemoryNewDeviceDefaultsToFHD(t *testing.T) {
	root, put := videoMemoryRoot(t)
	putImages(put, "pcie", "pcie-fhd-fixed", "pcie-qhd", "pcie-qhd-fixed", "pcie-uhd")
	put(dtBase+"nanokvm,video-memory-mode", "fhd\x00")
	put(dtBase+"reserved-memory/ion/size", "\x03\x80\x00\x00")
	s := readVideoMemoryStatus(root)
	if s.Active != "fhd" || s.Selected != "fhd" || s.SizeMiB != 56 || s.RebootRequired || !s.Available ||
		!slices.Equal(s.Modes, []string{"fhd", "fhd-fixed", "qhd", "qhd-fixed", "uhd"}) {
		t.Fatalf("new device: %+v", s)
	}
}

func TestVideoMemorySeparatesActiveAndSelected(t *testing.T) {
	root, put := videoMemoryRoot(t)
	putImages(put, "pcie", "pcie-fhd-fixed", "pcie-qhd", "pcie-qhd-fixed", "pcie-uhd")
	put(dtBase+"nanokvm,video-memory-mode", "fhd\x00")
	put("etc/kvm/video-memory-mode", "qhd-fixed\n")
	s := readVideoMemoryStatus(root)
	if !s.Available || !s.RebootRequired || s.Active != "fhd" || s.Selected != "qhd-fixed" {
		t.Fatalf("pending: %+v", s)
	}
	put(dtBase+"nanokvm,video-memory-mode", "qhd-fixed\x00")
	s = readVideoMemoryStatus(root)
	if s.RebootRequired || s.Active != "qhd-fixed" || s.Selected != "qhd-fixed" {
		t.Fatalf("booted: %+v", s)
	}
	// The saved selection wins over a running image that lost its file.
	put("etc/kvm/video-memory-mode", "bad\n")
	if s = readVideoMemoryStatus(root); s.Selected != "qhd-fixed" || s.RebootRequired {
		t.Fatalf("invalid selection: %+v", s)
	}
}

func TestVideoMemoryKeepsLegacyCapability(t *testing.T) {
	for _, tc := range []struct {
		running, active, selected string
		file                      string
		reboot                    bool
	}{
		// An upgraded device that has not run activation yet: no file, no change.
		{"cma", "qhd", "qhd", "", false},
		{"fixed", "qhd-fixed", "qhd-fixed", "", false},
		{"uhd", "uhd", "uhd", "", false},
		// Legacy selections are read as the same capability.
		{"cma", "qhd", "qhd", "cma\n", false},
		{"fixed", "qhd-fixed", "qhd-fixed", "fixed\n", false},
		// Activation wrote the new name: the image changes at the next boot.
		{"cma", "qhd", "qhd", "qhd\n", true},
		{"fixed", "qhd-fixed", "qhd-fixed", "qhd-fixed\n", true},
		{"cma", "qhd", "qhd-fixed", "fixed\n", true},
		{"cma", "qhd", "fhd", "fhd\n", true},
		{"uhd", "uhd", "uhd", "uhd\n", false},
	} {
		root, put := videoMemoryRoot(t)
		putImages(put, "pcie", "pcie-fhd-fixed", "pcie-qhd", "pcie-qhd-fixed", "pcie-uhd")
		put(dtBase+"nanokvm,video-memory-mode", tc.running+"\x00")
		put(dtBase+"reserved-memory/ion/size", "\x08\x00\x00\x00")
		if tc.file != "" {
			put("etc/kvm/video-memory-mode", tc.file)
		}
		s := readVideoMemoryStatus(root)
		if s.Active != tc.active || s.Selected != tc.selected || s.RebootRequired != tc.reboot || s.SizeMiB != 128 {
			t.Errorf("running %s, file %q: %+v", tc.running, tc.file, s)
		}
	}
}

func TestVideoMemoryBootImagesWithoutTheProperty(t *testing.T) {
	root, put := videoMemoryRoot(t)
	putImages(put, "pcie", "pcie-fhd-fixed", "pcie-qhd", "pcie-qhd-fixed", "pcie-uhd")
	if s := readVideoMemoryStatus(root); s.Active != "unknown" || s.Selected != "fhd" || s.RebootRequired {
		t.Fatalf("nothing known: %+v", s)
	}
	put(dtBase+"cvitek-ion/heap-carveout/nanokvm,cma-backend", "")
	if s := readVideoMemoryStatus(root); s.Active != "qhd" || s.Selected != "qhd" || s.RebootRequired {
		t.Fatalf("CMA: %+v", s)
	}
	put(dtBase+"reserved-memory/ion/compatible", "ion-region\x00")
	put(dtBase+"reserved-memory/ion/size", "\x08\x00\x00\x00")
	if err := os.RemoveAll(filepath.Join(root, dtBase, "cvitek-ion")); err != nil {
		t.Fatal(err)
	}
	put("etc/kvm/video-memory-mode", "uhd\n")
	if s := readVideoMemoryStatus(root); s.Active != "uhd" || s.Selected != "uhd" || s.SizeMiB != 128 || s.RebootRequired {
		t.Fatalf("128 MiB fixed: %+v", s)
	}
	put(dtBase+"reserved-memory/ion/size", "\x04\x00\x00\x00")
	if s := readVideoMemoryStatus(root); s.Active != "qhd-fixed" || s.SizeMiB != 64 || !s.RebootRequired {
		t.Fatalf("64 MiB fixed: %+v", s)
	}
}

func TestVideoMemoryModesFollowInstalledImages(t *testing.T) {
	root, put := videoMemoryRoot(t)
	put(dtBase+"nanokvm,video-memory-mode", "fhd\x00")
	putImages(put, "pcie", "pcie-qhd", "pcie-uhd")
	s := readVideoMemoryStatus(root)
	if !slices.Equal(s.Modes, []string{"fhd", "qhd", "uhd"}) || !s.Available {
		t.Fatalf("three images: %+v", s)
	}
	if err := os.Remove(filepath.Join(root, "usr/lib/nanokvm/boot/pcie-uhd.dtb")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "usr/lib/nanokvm/boot/pcie-qhd.dtb")); err != nil {
		t.Fatal(err)
	}
	if s = readVideoMemoryStatus(root); !slices.Equal(s.Modes, []string{"fhd"}) || s.Available {
		t.Fatalf("one image: %+v", s)
	}
	put(dtBase+"sipeed,board-revision", "detect\x00")
	if s = readVideoMemoryStatus(root); len(s.Modes) != 0 || s.Available {
		t.Fatalf("board without images: %+v", s)
	}
}

func TestVideoMemoryPreviousKernelPackageOffersNothing(t *testing.T) {
	root, put := videoMemoryRoot(t)
	// pcie.dtb is 128 MiB CMA there, not fhd.
	putImages(put, "pcie", "pcie-fixed", "pcie-uhd")
	put(dtBase+"nanokvm,video-memory-mode", "cma\x00")
	s := readVideoMemoryStatus(root)
	if len(s.Modes) != 0 || s.Available || s.Active != "qhd" || s.Selected != "qhd" {
		t.Fatalf("previous package: %+v", s)
	}
}

func TestVideoMemoryModeNames(t *testing.T) {
	for mode, want := range map[string]string{"fhd": "pcie.dtb", "fhd-fixed": "pcie-fhd-fixed.dtb", "qhd": "pcie-qhd.dtb",
		"qhd-fixed": "pcie-qhd-fixed.dtb", "uhd": "pcie-uhd.dtb"} {
		if !validVideoMemoryMode(mode) || videoMemoryImageName("pcie", mode) != want {
			t.Errorf("%s: %s", mode, videoMemoryImageName("pcie", mode))
		}
	}
	for _, mode := range []string{"", "auto", "cma", "fixed", "../fhd", "FHD", "uhd-fixed"} {
		if validVideoMemoryMode(mode) {
			t.Errorf("%q is not a mode", mode)
		}
	}
	for old, want := range map[string]string{"cma": "qhd", "fixed": "qhd-fixed", "uhd": "uhd", "fhd": "fhd", "qhd-fixed": "qhd-fixed", "auto": ""} {
		if got, _ := normalizeVideoMemoryMode(old); got != want {
			t.Errorf("%s -> %q, want %q", old, got, want)
		}
	}
}

// The pools the server assumes for each mode are what platform/build.sh
// makes, and the capture gates (common.VideoPool, kvm_vision.cpp) agree.
func TestVideoMemoryPoolsMatchTheGates(t *testing.T) {
	for _, tc := range []struct {
		mode             string
		mib              int
		reusable         bool
		qhd, portraitMax bool
		uhd              bool
	}{
		{"fhd", 52, true, false, false, false},
		{"fhd-fixed", 50, false, false, false, false},
		{"qhd", 68, true, true, true, false},
		{"qhd-fixed", 66, false, true, true, false},
		{"uhd", 118, false, true, true, true},
	} {
		pool, ok := videoMemoryPool(tc.mode)
		if !ok || pool.MiB != tc.mib || pool.Reusable != tc.reusable ||
			pool.QHD() != tc.qhd || pool.PortraitMax() != tc.portraitMax || pool.UHD() != tc.uhd {
			t.Errorf("%s: %+v", tc.mode, pool)
		}
	}
	if _, ok := videoMemoryPool("cma"); ok {
		t.Error("cma has no pool")
	}
}
