package main

import (
	"bytes"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const privateLibrariesFeature = "nkos-feature-private-libs-riscv64=1"

// The image supplies only musl. Every other DT_NEEDED object must be in this
// package's private lib directory. No system library replacement is permitted.
func validatePrivateELF(path, root, id string) error {
	magic := make([]byte, 4)
	raw, err := os.Open(path)
	if err != nil {
		return err
	}
	_, readErr := io.ReadFull(raw, magic)
	raw.Close()
	if readErr != nil || !bytes.Equal(magic, []byte{0x7f, 'E', 'L', 'F'}) {
		return nil
	}
	f, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != elf.EM_RISCV || (f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
		return fmt.Errorf("invalid private ELF architecture/type: %s", path)
	}
	dynamic := false
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			if p.Filesz > 128 {
				return fmt.Errorf("oversized ELF interpreter")
			}
			b, err := io.ReadAll(p.Open())
			if err != nil {
				return err
			}
			if string(b) != "/lib/ld-musl-riscv64.so.1\x00" {
				return fmt.Errorf("unsupported private ELF interpreter: %q", b)
			}
		}
		if p.Type == elf.PT_DYNAMIC {
			dynamic = true
		}
	}
	if !dynamic {
		return nil
	}
	// Require the section table too: debug/elf must not mistake a stripped-off
	// dynamic section for an executable without library dependencies.
	if f.SectionByType(elf.SHT_DYNAMIC) == nil {
		return fmt.Errorf("missing ELF dynamic section")
	}
	libraries, err := f.ImportedLibraries()
	if err != nil {
		return err
	}
	expected := "/opt/nkos/addons/" + id + "/lib"
	paths := []string{}
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		values, err := f.DynString(tag)
		if err != nil {
			return err
		}
		paths = append(paths, values...)
	}
	if len(paths) != 1 || paths[0] != expected {
		return fmt.Errorf("private ELF search path must be %s: %s", expected, path)
	}
	for _, name := range libraries {
		if name == "libc.so" {
			continue
		}
		if name == "" || filepath.Base(name) != name || strings.Contains(name, "..") {
			return fmt.Errorf("unsafe ELF dependency: %q", name)
		}
		dep := filepath.Join(root, "lib", name)
		info, err := os.Lstat(dep)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("missing private ELF dependency %s", name)
		}
		library, err := elf.Open(dep)
		if err != nil {
			return fmt.Errorf("invalid private library %s: %w", name, err)
		}
		good := library.Class == elf.ELFCLASS64 && library.Data == elf.ELFDATA2LSB && library.Machine == elf.EM_RISCV && library.Type == elf.ET_DYN
		library.Close()
		if !good {
			return fmt.Errorf("incompatible private library %s", name)
		}
	}
	return nil
}
