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
