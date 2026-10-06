package vm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVideoMemorySeparatesActiveAndSelected(t *testing.T) {
	root := t.TempDir()
	put := func(path, value string) {
		t.Helper()
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put("sys/firmware/devicetree/base/cvitek-ion/heap-carveout/nanokvm,cma-backend", "")
	put("sys/firmware/devicetree/base/sipeed,board-revision", "pcie\x00")
	put("usr/lib/nanokvm/boot/pcie.sd", "cma")
	s := readVideoMemoryStatus(root)
	if s.Active != "cma" || s.Selected != "cma" || s.Available || s.RebootRequired {
		t.Fatalf("legacy: %+v", s)
	}
	put("usr/lib/nanokvm/boot/pcie-fixed.sd", "fixed")
	put("etc/kvm/video-memory-mode", "fixed\n")
	s = readVideoMemoryStatus(root)
	if !s.Available || !s.RebootRequired || s.Active != "cma" || s.Selected != "fixed" {
		t.Fatalf("pending: %+v", s)
	}
	put("sys/firmware/devicetree/base/nanokvm,video-memory-mode", "fixed\x00")
	s = readVideoMemoryStatus(root)
	if s.RebootRequired || s.Active != "fixed" {
		t.Fatalf("booted: %+v", s)
	}
}

func TestVideoMemoryUHDMode(t *testing.T) {
	root := t.TempDir()
	put := func(path, value string) {
		t.Helper()
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put("sys/firmware/devicetree/base/sipeed,board-revision", "pcie\x00")
	for _, name := range []string{"pcie.sd", "pcie-fixed.sd", "pcie-uhd.sd"} {
		put("usr/lib/nanokvm/boot/"+name, "fit")
	}
	// An older boot image without the mode property: a 128 MiB fixed region is uhd.
	put("sys/firmware/devicetree/base/reserved-memory/ion/compatible", "ion-region\x00")
	put("sys/firmware/devicetree/base/reserved-memory/ion/size", "\x08\x00\x00\x00")
	put("etc/kvm/video-memory-mode", "uhd\n")
	s := readVideoMemoryStatus(root)
	if s.Active != "uhd" || s.Selected != "uhd" || s.SizeMiB != 128 || s.RebootRequired ||
		!s.Available || len(s.Modes) != 3 || s.Modes[2] != "uhd" {
		t.Fatalf("uhd: %+v", s)
	}
	put("sys/firmware/devicetree/base/reserved-memory/ion/size", "\x04\x00\x00\x00")
	if s = readVideoMemoryStatus(root); s.Active != "fixed" || s.SizeMiB != 64 || !s.RebootRequired {
		t.Fatalf("fixed 64 MiB: %+v", s)
	}
	if err := os.Remove(filepath.Join(root, "usr/lib/nanokvm/boot/pcie-uhd.sd")); err != nil {
		t.Fatal(err)
	}
	if s = readVideoMemoryStatus(root); len(s.Modes) != 2 || !validVideoMemoryMode("uhd") || validVideoMemoryMode("auto") {
		t.Fatalf("modes without the uhd image: %+v", s)
	}
}
