package standby

import (
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type elfStager struct {
	sourceRoot string
	targetRoot string
	files      map[string]bool
}

func stageELF(name, root string, seen map[string]bool) error {
	stager := elfStager{sourceRoot: "/", targetRoot: root, files: seen}
	return stager.stage(name)
}

func (s *elfStager) stage(name string) error {
	// Walk each executable's graph separately: another executable's loader
	// cannot satisfy this one's imports. Copying and RAM accounting still
	// share the files map across all staged executables.
	return s.walk(name, "", make(map[string]bool))
}

func (s *elfStager) walk(name, interpreter string, visited map[string]bool) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return fmt.Errorf("unsupported ELF path %s", name)
	}
	if visited[name] {
		return nil
	}
	visited[name] = true
	source := filepath.Join(s.sourceRoot, name)
	f, err := elf.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		b, err := io.ReadAll(p.Open())
		if err != nil {
			return err
		}
		interpreter = strings.TrimRight(string(b), "\x00")
		if err := s.walk(interpreter, interpreter, visited); err != nil {
			return err
		}
	}
	if !s.files[name] {
		if s.targetRoot != "" {
			if err := copyFile(source, filepath.Join(s.targetRoot, name), 0700); err != nil {
				return err
			}
		}
		s.files[name] = true
	}
	libs, err := f.ImportedLibraries()
	if err != nil {
		return err
	}
	for _, lib := range libs {
		if strings.Contains(lib, "/") {
			return fmt.Errorf("unsupported ELF dependency %s", lib)
		}
		if muslSelfLibrary(interpreter, lib) {
			continue
		}
		found := ""
		for _, base := range []string{"/lib", "/usr/lib"} {
			candidate := base + "/" + lib
			if _, err := os.Stat(filepath.Join(s.sourceRoot, candidate)); err == nil {
				found = candidate
				break
			}
		}
		if found == "" {
			return fmt.Errorf("RAM dependency missing: %s", lib)
		}
		if err := s.walk(found, interpreter, visited); err != nil {
			return err
		}
	}
	return nil
}

func muslSelfLibrary(interpreter, name string) bool {
	base := filepath.Base(interpreter)
	if filepath.Dir(interpreter) != "/lib" || !strings.HasPrefix(base, "ld-musl-") ||
		!strings.HasSuffix(base, ".so.1") || len(base) <= len("ld-musl-.so.1") {
		return false
	}
	// Match musl's load_library reserved prefixes. libcrypt is deliberately
	// absent: it is a separate library, unlike libc/libm/libpthread and peers.
	for _, prefix := range []string{"libc.", "libpthread.", "librt.", "libm.", "libdl.", "libutil.", "libxnet."} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
