package main

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPEM = "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAb+MgvY1Dd9KkoDbMFkMOxb0zeRvSgk6zGttBklheWXA=\n-----END PUBLIC KEY-----\n"

func testContract() targetContract {
	return targetContract{
		Format: 1, BaseABI: "1.0.0", ServerAPI: 1,
		Features:     []string{"nkos-feature-shell=1"},
		Trust:        trustSpec{Keys: []keySpec{{ID: "release", Filename: "release.pem", PublicPEM: testPEM}}},
		Repositories: []repositorySpec{{URL: "https://example.invalid/repository", KeyIDs: []string{"release"}}},
	}
}

func writeFixture(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, value, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestContractEmbedsTargetTrustAndRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "contract.json")
	writeFixture(t, path, testContract())
	m := &manager{}
	if _, err := m.loadContract(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"format":1,"base_abi":"1.0.0","server_api":1,"features":["nkos-feature-shell=1"],"trust":{"keys":[]},"repositories":[],"world":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.loadContract(path); err == nil {
		t.Fatal("accepted transient world or empty trust in immutable contract")
	}
}

func TestABIVersionAndAPKVersionGrammarsAreSeparate(t *testing.T) {
	for _, value := range []string{"2.3-r1", "1.4.10-r1", "1.4.10_rc1-r0"} {
		if !apkVersionRE.MatchString(value) {
			t.Fatalf("native APK version %q was rejected", value)
		}
	}
	for _, value := range []string{"bad-r0", "1.2.3+meta-r1", "1.2.3~rc-r1", "1.2.3/r1"} {
		if apkVersionRE.MatchString(value) {
			t.Fatalf("invalid native APK version %q was accepted", value)
		}
	}
	if !abiVersionRE.MatchString("1.0.0") || abiVersionRE.MatchString("1.0.0-beta.6") || abiVersionRE.MatchString("2.3") {
		t.Fatal("ABI grammar is not the independent three-component contract")
	}
}

func TestReadWorldPreservesConstraintsAndFiltersImageProviders(t *testing.T) {
	root := t.TempDir()
	world := filepath.Join(root, "etc/apk/world")
	if err := os.MkdirAll(filepath.Dir(world), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(world, []byte("nkos-base-abi=1.0.0\nnkos-server-api=1\nnkos-feature-shell=1\nnkos-addon-demo>=1.2-r0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &manager{root: root}
	got, err := m.readWorld()
	if err != nil || len(got) != 1 || got[0] != "nkos-addon-demo>=1.2-r0" {
		t.Fatalf("world=%v err=%v", got, err)
	}
}

func TestReplaceAddonIntentUpdatesPinsWithoutDroppingConstraints(t *testing.T) {
	got := replaceAddonIntent([]string{"nkos-addon-demo=1.4.10-r1", "nkos-addon-other>=2.3-r0"}, "nkos-addon-demo=1.4.10-r2", "demo", "1.4.10-r2")
	if strings.Join(got, "\n") != "nkos-addon-demo=1.4.10-r2\nnkos-addon-other>=2.3-r0" {
		t.Fatalf("updated world=%v", got)
	}
	got = replaceAddonIntent([]string{"nkos-addon-demo>=1.0.0-r0"}, "nkos-addon-demo", "demo", "2.3-r1")
	if strings.Join(got, "\n") != "nkos-addon-demo>=1.0.0-r0" {
		t.Fatalf("dropped non-pinned constraint: %v", got)
	}
}

func TestPrepareAndVerifyUpgradeBindExactBytes(t *testing.T) {
	root, state, stage := t.TempDir(), t.TempDir(), t.TempDir()
	world := filepath.Join(root, "etc/apk/world")
	if err := os.MkdirAll(filepath.Dir(world), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(world, []byte("nkos-addon-demo>=1.2-r0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	contractFile := filepath.Join(t.TempDir(), "contract.json")
	writeFixture(t, contractFile, testContract())
	fake := filepath.Join(t.TempDir(), "apk")
	script := `#!/bin/sh
set -eu
all=" $* "
cache=
while [ "$#" -gt 0 ]; do
 if [ "$1" = --cache-dir ]; then cache=$2; break; fi
 shift
done
case "$all" in
 *" add "*" nkos-addon-demo"*) printf signed-fixture > "$cache/nkos-addon-demo-1.2.0-r0.deadbeef.apk" ;;
 *" add "*) exit 0 ;;
 *" query "*) printf '%s\n' '[{"name":"nkos-base-abi","version":"1.0.0","description":"virtual meta package","arch":"noarch","contents":[]},{"name":"nkos-server-api","version":"1","description":"virtual meta package","arch":"noarch","contents":[]},{"name":"nkos-feature-shell","version":"1","description":"virtual meta package","arch":"noarch","contents":[]},{"name":"nkos-addon-demo","version":"1.2.0-r0","arch":"riscv64","installed-size":14,"contents":[]}]';;
 *" verify "*) exit 0 ;;
 *" adbdump "*) printf '%s\n' '{"info":{"name":"nkos-addon-demo","version":"1.2.0-r0","arch":"riscv64"},"paths":[]}' ;;
 *) exit 9 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	m := &manager{root: root, state: state, apk: fake, scratch: filepath.Join(t.TempDir(), "resolver")}
	if err := m.prepare(contractFile, stage); err != nil {
		t.Fatal(err)
	}
	var checkpoint restoreCheckpoint
	if err := readJSON(filepath.Join(stage, "checkpoint.json"), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if strings.Join(checkpoint.World, "") != "nkos-addon-demo>=1.2-r0" || len(checkpoint.Packages) != 1 || checkpoint.Packages[0].Name != "nkos-addon-demo" {
		t.Fatalf("bad checkpoint: %#v", checkpoint)
	}
	if err := m.verifyUpgrade(contractFile, stage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(world, []byte("nkos-addon-demo=1.2.0-r0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.verifyUpgrade(contractFile, stage); err == nil {
		t.Fatal("accepted addon constraint change after preflight")
	}
	if err := os.WriteFile(world, []byte("nkos-addon-demo>=1.2-r0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, checkpoint.Packages[0].Path), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.verifyUpgrade(contractFile, stage); err == nil {
		t.Fatal("accepted changed cached APK bytes")
	}
}

func TestEmptyCheckpointNeedsNoAPKBinary(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/apk"), 0700); err != nil {
		t.Fatal(err)
	}
	contractFile := filepath.Join(t.TempDir(), "contract.json")
	writeFixture(t, contractFile, testContract())
	m := &manager{root: root, state: t.TempDir(), apk: "/missing/apk"}
	if err := m.prepare(contractFile, stage); err != nil {
		t.Fatal(err)
	}
	if err := m.verifyUpgrade(contractFile, stage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/apk/world"), []byte("nkos-addon-unexpected=1.0.0-r0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.verifyUpgrade(contractFile, stage); err == nil {
		t.Fatal("accepted nonempty addon world after empty preflight")
	}
}

type elfFixtureOptions struct {
	class   elf.Class
	data    elf.Data
	machine elf.Machine
	elfType elf.Type
	interp  bool
	needed  bool
 neededName string
 runpath string
}

func testELF64(options elfFixtureOptions) []byte {
	if options.class == 0 {
		options.class = elf.ELFCLASS64
	}
	if options.data == 0 {
		options.data = elf.ELFDATA2LSB
	}
	if options.machine == 0 {
		options.machine = elf.EM_RISCV
	}
	if options.elfType == 0 {
		options.elfType = elf.ET_EXEC
	}
	order := binary.ByteOrder(binary.LittleEndian)
	if options.data == elf.ELFDATA2MSB {
		order = binary.BigEndian
	}
	neededName := options.neededName
 if neededName == "" { neededName = "libc.so.6" }
 dynstr := append(append([]byte{0}, []byte(neededName)...), 0)
 runpathOffset := len(dynstr)
 dynstr = append(append(dynstr, []byte(options.runpath)...), 0)
 dynamicSize := 32
 if options.runpath != "" { dynamicSize += 16 }
 const headerSize = 64
	const programSize = 56
	const sectionSize = 64
	programCount := 0
	size := headerSize
	if options.interp || options.needed {
		programCount = 1
		size += programSize
	}
	dynamicOffset := 0
	stringOffset := 0
	sectionOffset := 0
	if options.needed {
		if size%8 != 0 {
			size += 8 - size%8
		}
		dynamicOffset = size
		size += dynamicSize
		stringOffset = size
		size += len(dynstr)
		if size%8 != 0 {
			size += 8 - size%8
		}
		sectionOffset = size
		size += 3 * sectionSize
	}
	data := make([]byte, size)
	data[0], data[1], data[2], data[3] = 0x7f, 'E', 'L', 'F'
	data[4], data[5], data[6] = byte(options.class), byte(options.data), 1
	put16 := func(offset int, value uint16) { order.PutUint16(data[offset:offset+2], value) }
	put32 := func(offset int, value uint32) { order.PutUint32(data[offset:offset+4], value) }
	put64 := func(offset int, value uint64) { order.PutUint64(data[offset:offset+8], value) }
	put16(16, uint16(options.elfType))
	put16(18, uint16(options.machine))
	put32(20, 1)
	put64(32, uint64(headerSize))
	put16(52, headerSize)
	put16(54, programSize)
	put16(56, uint16(programCount))
	if options.needed {
		put64(40, uint64(sectionOffset))
		put16(58, sectionSize)
		put16(60, 3)
		put16(62, 0)
	}
	if options.interp {
		put32(headerSize, uint32(elf.PT_INTERP))
		put32(headerSize+4, uint32(elf.PF_R))
	}
	if options.needed {
		put32(headerSize, uint32(elf.PT_DYNAMIC))
		put64(headerSize+8, uint64(dynamicOffset))
		put64(headerSize+32, uint64(dynamicSize))
		put64(headerSize+40, uint64(dynamicSize))
	}
	if options.needed {
		put64(dynamicOffset, uint64(elf.DT_NEEDED))
		put64(dynamicOffset+8, 1)
		put64(dynamicOffset+16, uint64(elf.DT_NULL))
		if options.runpath != "" {
   put64(dynamicOffset+16, uint64(elf.DT_RUNPATH))
   put64(dynamicOffset+24, uint64(runpathOffset))
   put64(dynamicOffset+32, uint64(elf.DT_NULL))
  }
  copy(data[stringOffset:], dynstr)
		section := sectionOffset + sectionSize
		put32(section+4, uint32(elf.SHT_DYNAMIC))
		put64(section+24, uint64(dynamicOffset))
		put64(section+32, uint64(dynamicSize))
		put32(section+40, 2)
		put64(section+56, 16)
		section = sectionOffset + 2*sectionSize
		put32(section+4, uint32(elf.SHT_STRTAB))
		put64(section+24, uint64(stringOffset))
		put64(section+32, uint64(len(dynstr)))
	}
	return data
}

func testELF32() []byte {
	data := make([]byte, 52)
	data[0], data[1], data[2], data[3] = 0x7f, 'E', 'L', 'F'
	data[4], data[5], data[6] = byte(elf.ELFCLASS32), byte(elf.ELFDATA2LSB), 1
	binary.LittleEndian.PutUint16(data[16:18], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(data[18:20], uint16(elf.EM_RISCV))
	binary.LittleEndian.PutUint32(data[20:24], 1)
	binary.LittleEndian.PutUint16(data[40:42], 52)
	binary.LittleEndian.PutUint16(data[42:44], 32)
	binary.LittleEndian.PutUint16(data[46:48], 40)
	return data
}

func writeExecutableFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateStaticELFFixtures(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		ok   bool
	}{
		{name: "static-riscv64", data: testELF64(elfFixtureOptions{}), ok: true},
		{name: "static-pie-riscv64", data: testELF64(elfFixtureOptions{elfType: elf.ET_DYN}), ok: true},
		{name: "dynamic-interpreter", data: testELF64(elfFixtureOptions{elfType: elf.ET_DYN, interp: true})},
		{name: "dynamic-needed", data: testELF64(elfFixtureOptions{elfType: elf.ET_DYN, needed: true})},
		{name: "wrong-machine", data: testELF64(elfFixtureOptions{machine: elf.EM_X86_64})},
		{name: "wrong-endian", data: testELF64(elfFixtureOptions{data: elf.ELFDATA2MSB})},
		{name: "wrong-class", data: testELF32()},
		{name: "malformed-elf", data: []byte{0x7f, 'E', 'L', 'F'}},
		{name: "approved-shell", data: []byte("#!/bin/sh\nexit 0\n"), ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeExecutableFixture(t, test.name, test.data)
			err := validateStaticELF(path)
			if test.ok && err != nil {
				t.Fatalf("valid executable rejected: %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("invalid executable accepted")
			}
		})
	}
}

func TestValidatePackageMetadataRejectsNativePayloadBeforeWrites(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "apk")
	script := "#!/bin/sh\n" +
		"set -eu\n" +
		"if [ \"${1:-}\" = adbdump ]; then\n" +
		"cat <<'JSON'\n" +
		"{\"info\":{\"name\":\"nkos-addon-demo\",\"version\":\"1.0.0-r0\",\"arch\":\"riscv64\"},\"paths\":[{\"name\":\"addons/demo/bin\",\"files\":[{\"name\":\"demo\",\"acl\":{\"mode\":493}}]}]}\n" +
		"JSON\n" +
		"exit 0\n" +
		"fi\n" +
		"case \" $* \" in\n" +
		"  *\" add \"*)\n" +
		"    destination=\n" +
		"    previous=\n" +
		"    for argument in \"$@\"; do\n" +
		"      if [ \"$previous\" = --root ]; then destination=$argument; fi\n" +
		"      previous=$argument\n" +
		"    done\n" +
		"    mkdir -p \"$destination/addons/demo/bin\"\n" +
		"    printf data > \"$destination/addons/demo/config\"\n" +
		"    chmod 0644 \"$destination/addons/demo/config\"\n" +
		"    cp \"$NKOS_TEST_ELF\" \"$destination/addons/demo/bin/demo\"\n" +
		"    chmod 0755 \"$destination/addons/demo/bin/demo\"\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fake, 0755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	marker := filepath.Join(root, "transaction-marker")
	if err := os.WriteFile(marker, []byte("unchanged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &manager{root: root, state: t.TempDir(), apk: fake}
	packagePath := filepath.Join(t.TempDir(), "demo.apk")
	if err := os.WriteFile(packagePath, []byte("test-package"), 0600); err != nil {
		t.Fatal(err)
	}
	expected := installedPackage{Name: "nkos-addon-demo", Version: "1.0.0-r0", Arch: "riscv64"}

	t.Setenv("NKOS_TEST_ELF", writeExecutableFixture(t, "dynamic", testELF64(elfFixtureOptions{elfType: elf.ET_DYN, needed: true})))
	if err := m.validatePackageMetadata(packagePath, expected); err == nil {
		t.Fatal("dynamic native payload passed shared package validation")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "unchanged\n" {
		t.Fatalf("prewrite state changed: %q %v", got, err)
	}

	t.Setenv("NKOS_TEST_ELF", writeExecutableFixture(t, "shell", []byte("#!/bin/sh\nexit 0\n")))
	if err := m.validatePackageMetadata(packagePath, expected); err != nil {
		t.Fatalf("approved executable script rejected: %v", err)
	}
}
