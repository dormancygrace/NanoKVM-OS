package standby

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Minimal real ELF64 files exercise debug/elf and recursive staging, including
// transitive DT_NEEDED entries, without relying on the host libc or toolchain.
func writeELFFixture(t *testing.T, root, name, interpreter string, needed ...string) {
	t.Helper()
	h := elf.Header64{Ident: [16]byte{0x7f, 'E', 'L', 'F', 2, 1, 1},
		Type: uint16(elf.ET_DYN), Machine: uint16(elf.EM_RISCV), Version: 1,
		Ehsize: 64, Phentsize: 56, Shentsize: 64, Shnum: 3}
	if interpreter != "" {
		h.Phnum = 1
		h.Phoff = 64
	}
	data := bytes.NewBuffer(make([]byte, 64+int(h.Phnum)*56))
	interpOffset := uint64(data.Len())
	if interpreter != "" {
		data.WriteString(interpreter)
		data.WriteByte(0)
	}
	strtab := bytes.NewBuffer([]byte{0})
	offsets := make([]uint64, len(needed))
	for i, name := range needed {
		offsets[i] = uint64(strtab.Len())
		strtab.WriteString(name)
		strtab.WriteByte(0)
	}
	strOffset := uint64(data.Len())
	data.Write(strtab.Bytes())
	dynOffset := uint64(data.Len())
	write := func(buffer *bytes.Buffer, value any) {
		t.Helper()
		if err := binary.Write(buffer, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, offset := range offsets {
		write(data, elf.Dyn64{Tag: int64(elf.DT_NEEDED), Val: offset})
	}
	write(data, elf.Dyn64{Tag: int64(elf.DT_NULL)})
	dynSize := uint64(data.Len()) - dynOffset
	h.Shoff = uint64(data.Len())
	write(data, elf.Section64{})
	write(data, elf.Section64{Type: uint32(elf.SHT_STRTAB), Off: strOffset, Size: uint64(strtab.Len()), Addralign: 1})
	write(data, elf.Section64{Type: uint32(elf.SHT_DYNAMIC), Off: dynOffset, Size: dynSize, Link: 1, Addralign: 8, Entsize: 16})
	var header bytes.Buffer
	write(&header, h)
	copy(data.Bytes(), header.Bytes())
	if interpreter != "" {
		var program bytes.Buffer
		write(&program, elf.Prog64{Type: uint32(elf.PT_INTERP), Flags: uint32(elf.PF_R), Off: interpOffset, Filesz: uint64(len(interpreter) + 1), Memsz: uint64(len(interpreter) + 1), Align: 1})
		copy(data.Bytes()[64:], program.Bytes())
	}
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestStageELFMuslAliasesAcrossTransitiveDependencies(t *testing.T) {
	source, destination := t.TempDir(), t.TempDir()
	loader := "/lib/ld-musl-riscv64.so.1"
	writeELFFixture(t, source, loader, "")
	writeELFFixture(t, source, "/sbin/wpa_supplicant", loader, "libssl.so.3", "libnl.so")
	writeELFFixture(t, source, "/usr/lib/libssl.so.3", "", "libcrypto.so.3", "libc.so")
	writeELFFixture(t, source, "/usr/lib/libcrypto.so.3", "", "libpthread.so.0", "libm.so.6", "libdl.so.2", "librt.so.1", "libutil.so.1", "libxnet.so.1")
	writeELFFixture(t, source, "/lib/libnl.so", "", "libc.musl-riscv64.so.1")
	// musl resolves its reserved names to the interpreter even when another
	// file exists at that name. It must not enter the payload or RAM budget.
	if err := os.WriteFile(filepath.Join(source, "lib/libm.so.6"), []byte("not ELF"), 0600); err != nil {
		t.Fatal(err)
	}
	planned := map[string]bool{}
	planner := elfStager{sourceRoot: source, files: planned}
	if err := planner.stage("/sbin/wpa_supplicant"); err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{loader: true, "/sbin/wpa_supplicant": true, "/usr/lib/libssl.so.3": true, "/usr/lib/libcrypto.so.3": true, "/lib/libnl.so": true}
	if !reflect.DeepEqual(planned, expected) {
		t.Fatal(planned)
	}
	copied := map[string]bool{}
	copier := elfStager{sourceRoot: source, targetRoot: destination, files: copied}
	if err := copier.stage("/sbin/wpa_supplicant"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copied, planned) {
		t.Fatal(copied)
	}
	for name := range copied {
		want, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal(name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "lib/libm.so.6")); !os.IsNotExist(err) {
		t.Fatal("copied a self alias", err)
	}
}
