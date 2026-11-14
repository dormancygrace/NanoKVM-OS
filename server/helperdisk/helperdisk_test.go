package helperdisk

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// readFile finds a file by its long name and follows its FAT chain, as a
// computer does.
func readFile(t *testing.T, image []byte, name string) []byte {
	t.Helper()
	root := image[(reservedSectors+fats*sectorsPerFAT)*sector:]
	fat := image[reservedSectors*sector:]
	entry := func(n int) int {
		o := n / 2 * 3
		if n%2 == 0 {
			return int(fat[o]) | int(fat[o+1]&0x0F)<<8
		}
		return int(fat[o+1])>>4 | int(fat[o+2])<<4
	}
	long := ""
	for i := 0; i < rootEntries; i++ {
		e := root[i*32 : i*32+32]
		if e[11] == 0x0F {
			// A part of a long name; the parts come last first.
			part := ""
			for _, offset := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
				if u := binary.LittleEndian.Uint16(e[offset:]); u != 0 && u != 0xFFFF {
					part += string(rune(u))
				}
			}
			long = part + long
			continue
		}
		found := long
		long = ""
		if found != name {
			continue
		}
		size := int(binary.LittleEndian.Uint32(e[28:]))
		var data []byte
		for c := int(binary.LittleEndian.Uint16(e[26:])); c >= 2 && c < 0xFF8; c = entry(c) {
			start := (dataSector + (c-2)*sectorsPerCluster) * sector
			data = append(data, image[start:start+sectorsPerCluster*sector]...)
		}
		return data[:size]
	}
	t.Fatalf("no %q in the root directory", name)
	return nil
}

func TestBuildHoldsTheFiles(t *testing.T) {
	big := bytes.Repeat([]byte("sensor agent "), 600000) // 7.8 MB, many clusters
	script := []byte("#!/bin/sh\necho hello\n")
	files := []File{{"NanoKVM-Serial-Console-Setup-Linux.sh", script}, {"NanoKVM-Sensors-Setup-Windows.exe", big}, {"README.txt", nil}}
	image, err := Build(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(image) != Size || image[510] != 0x55 || image[511] != 0xAA || string(image[54:59]) != "FAT12" {
		t.Fatalf("not a FAT12 image of %d bytes", Size)
	}
	if dataClusters >= 4085 {
		t.Fatalf("%d clusters is not FAT12", dataClusters)
	}
	if !bytes.Equal(readFile(t, image, files[0].Name), script) || !bytes.Equal(readFile(t, image, files[1].Name), big) ||
		len(readFile(t, image, "README.txt")) != 0 {
		t.Fatal("a file reads back differently")
	}
	// Both FAT copies agree, and the same files give the same image.
	a := image[reservedSectors*sector : (reservedSectors+sectorsPerFAT)*sector]
	b := image[(reservedSectors+sectorsPerFAT)*sector : (reservedSectors+2*sectorsPerFAT)*sector]
	again, _ := Build(files)
	if !bytes.Equal(a, b) || !bytes.Equal(image, again) {
		t.Fatal("FAT copies or repeated builds differ")
	}
	other, _ := Build([]File{{files[0].Name, append(script, '\n')}})
	if bytes.Equal(image[39:43], other[39:43]) {
		t.Fatal("the volume id does not follow the contents")
	}
}

func TestBuildRefuses(t *testing.T) {
	for name, files := range map[string][]File{
		"a space": {{"NanoKVM Sensors.exe", nil}},
		"a path":  {{"dir/setup.sh", nil}},
		"twice":   {{"A.B", nil}, {"a.b", nil}},
		"too big": {{"BIG.BIN", make([]byte, Size)}},
	} {
		if _, err := Build(files); err == nil {
			t.Errorf("%s: built", name)
		}
	}
}
