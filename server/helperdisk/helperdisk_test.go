package helperdisk

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// readFile follows the directory entry and the FAT chain of an image, as a
// computer does.
func readFile(t *testing.T, image []byte, name11 string) []byte {
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
	for i := 0; i < rootEntries; i++ {
		e := root[i*32 : i*32+32]
		if string(e[:11]) != name11 {
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
	t.Fatalf("no %q in the root directory", name11)
	return nil
}

func TestBuildHoldsTheFiles(t *testing.T) {
	big := bytes.Repeat([]byte("sensor agent "), 600000) // 7.8 MB, many clusters
	script := []byte("#!/bin/sh\necho hello\n")
	image, err := Build([]File{{"SETUP.SH", script}, {"SENSORS.EXE", big}, {"README.TXT", nil}})
	if err != nil {
		t.Fatal(err)
	}
	if len(image) != Size || image[510] != 0x55 || image[511] != 0xAA || string(image[54:59]) != "FAT12" {
		t.Fatalf("not a FAT12 image of %d bytes", Size)
	}
	if dataClusters >= 4085 {
		t.Fatalf("%d clusters is not FAT12", dataClusters)
	}
	if !bytes.Equal(readFile(t, image, "SETUP   SH "), script) || !bytes.Equal(readFile(t, image, "SENSORS EXE"), big) ||
		len(readFile(t, image, "README  TXT")) != 0 {
		t.Fatal("a file reads back differently")
	}
	// Both FAT copies agree, and the same files give the same image.
	a := image[reservedSectors*sector : (reservedSectors+sectorsPerFAT)*sector]
	b := image[(reservedSectors+sectorsPerFAT)*sector : (reservedSectors+2*sectorsPerFAT)*sector]
	again, _ := Build([]File{{"SETUP.SH", script}, {"SENSORS.EXE", big}, {"README.TXT", nil}})
	if !bytes.Equal(a, b) || !bytes.Equal(image, again) {
		t.Fatal("FAT copies or repeated builds differ")
	}
	other, _ := Build([]File{{"SETUP.SH", append(script, '\n')}})
	if bytes.Equal(image[39:43], other[39:43]) {
		t.Fatal("the volume id does not follow the contents")
	}
}

func TestBuildRefuses(t *testing.T) {
	for name, files := range map[string][]File{
		"long name":  {{"NANOKVM-SENSORS.EXE", nil}},
		"lower case": {{"setup.sh", nil}},
		"twice":      {{"A.B", nil}, {"A.B", nil}},
		"too big":    {{"BIG.BIN", make([]byte, Size)}},
	} {
		if _, err := Build(files); err == nil {
			t.Errorf("%s: built", name)
		}
	}
}
