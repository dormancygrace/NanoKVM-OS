package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHevcDirectSetting(t *testing.T) {
	dir := t.TempDir()
	saved, savedParam := HevcDirectSettingFile, hevcDirectDriverParam
	t.Cleanup(func() { HevcDirectSettingFile, hevcDirectDriverParam = saved, savedParam })
	HevcDirectSettingFile = filepath.Join(dir, "hevc_vi_direct")
	hevcDirectDriverParam = filepath.Join(dir, "nk_packed_src")

	if HevcDirect() != 1 {
		t.Fatal("direct capture must be the default")
	}
	for content, want := range map[string]int{"0": 0, "0\n": 0, "1": 1, "": 1} {
		if err := os.WriteFile(HevcDirectSettingFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := HevcDirect(); got != want {
			t.Fatalf("setting %q: got %d, want %d", content, got, want)
		}
	}

	if HevcDirectSupported() {
		t.Fatal("no driver parameter must mean unsupported")
	}
	if err := os.WriteFile(hevcDirectDriverParam, []byte("1\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if !HevcDirectSupported() {
		t.Fatal("nk_packed_src=1 must mean supported")
	}
}

func TestViDirectH264Supported(t *testing.T) {
	dir := t.TempDir()
	saved := viYuv420DriverParam
	t.Cleanup(func() { viYuv420DriverParam = saved })
	viYuv420DriverParam = filepath.Join(dir, "nk_yuv420")

	if ViDirectH264Supported() {
		t.Fatal("no VI parameter must mean unsupported")
	}
	// The parameter's value is the capture mode libkvm chose, not the capability.
	for _, value := range []string{"N\n", "Y\n"} {
		if err := os.WriteFile(viYuv420DriverParam, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
		if !ViDirectH264Supported() {
			t.Fatalf("nk_yuv420=%q must mean supported", value)
		}
	}
}
