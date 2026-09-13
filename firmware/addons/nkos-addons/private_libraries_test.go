package main

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateLibraryClosure(t *testing.T) {
	for _, tc := range []struct {
		name, needed, runpath string
		library               bool
		machine               elf.Machine
		ok                    bool
	}{
		{"image-musl", "libc.so", "/opt/nkos/addons/demo/lib", false, elf.EM_RISCV, true},
		{"bundled-library", "libfoo.so", "/opt/nkos/addons/demo/lib", true, elf.EM_RISCV, true},
		{"missing-library", "libfoo.so", "/opt/nkos/addons/demo/lib", false, elf.EM_RISCV, false},
		{"external-search-path", "libc.so", "/usr/lib", false, elf.EM_RISCV, false},
		{"other-addon", "libc.so", "/opt/nkos/addons/other/lib", false, elf.EM_RISCV, false},
		{"dependency-escape", "../libfoo.so", "/opt/nkos/addons/demo/lib", true, elf.EM_RISCV, false},
		{"wrong-arch", "libc.so", "/opt/nkos/addons/demo/lib", false, elf.EM_X86_64, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			os.Mkdir(filepath.Join(root, "lib"), 0755)
			if tc.library {
				os.WriteFile(filepath.Join(root, "lib/libfoo.so"), testELF64(elfFixtureOptions{elfType: elf.ET_DYN}), 0644)
			}
			path := filepath.Join(root, "program")
			os.WriteFile(path, testELF64(elfFixtureOptions{needed: true, neededName: tc.needed, runpath: tc.runpath, machine: tc.machine}), 0755)
			err := validatePrivateELF(path, root, "demo")
			if (err == nil) != tc.ok {
				t.Fatalf("accepted=%v want=%v: %v", err == nil, tc.ok, err)
			}
		})
	}
}
func TestExportedPrivatePayloads(t *testing.T) {
	root := os.Getenv("NKOS_TEST_OPTIONAL_INPUT")
	if root == "" {
		t.Skip("set NKOS_TEST_OPTIONAL_INPUT for source-built payload gate")
	}
	for _, id := range []string{"mc", "superfile", "nano", "htop", "tcpdump", "ethtool", "bluez5-utils"} {
		dir := filepath.Join(root, id)
		err := filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				return nil
			}
			return validatePrivateELF(path, dir, id)
		})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
}
