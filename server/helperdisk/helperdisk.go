// Package helperdisk makes the helper disk on the device: the small read-only
// FAT disk the NanoKVM shows to the computer next to the virtual drive, with
// the programs to run there. The image with the serial console helper alone
// is made when NanoKVM OS is built (scripts/build-serial-console-disk.py);
// an add-on that brings a program for the computer, such as the computer
// sensors agent, cannot be in that one, so the disk is put together here from
// what is installed. Same layout as the build's: no partition table, FAT12,
// files in the root directory (long names as VFAT entries), fixed timestamps,
// so the same files give the same bytes.
package helperdisk

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

const (
	sector            = 512
	sectorsPerCluster = 8 // 4 KiB
	// 12 MiB: room for the agents, and few enough clusters for FAT12.
	totalSectors    = 24576
	reservedSectors = 1
	fats            = 2
	rootEntries     = 224
	sectorsPerFAT   = 9
	media           = 0xF8
	rootSectors     = rootEntries * 32 / sector
	dataSector      = reservedSectors + fats*sectorsPerFAT + rootSectors
	dataClusters    = (totalSectors - dataSector) / sectorsPerCluster
	// Size is the image's size in bytes.
	Size = totalSectors * sector

	fatDate     = (2026-1980)<<9 | 1<<5 | 1 // 2026-01-01
	attrArchive = 0x20
	attrLabel   = 0x08
	label       = "NANOKVM    "
)

// The stub mkfs.fat writes: a BIOS that tries to boot from the disk prints
// the message, waits for a key and goes on with the next device.
var bootCode = append([]byte{0x0e, 0x1f, 0xbe, 0x5b, 0x7c, 0xac, 0x22, 0xc0, 0x74, 0x0b, 0x56, 0xb4, 0x0e, 0xbb, 0x07, 0x00,
	0xcd, 0x10, 0x5e, 0xeb, 0xf0, 0x32, 0xe4, 0xcd, 0x16, 0xcd, 0x19, 0xeb, 0xfe},
	"This is not a bootable disk. It holds the NanoKVM helper programs.\r\nRemove it from the boot order and press any key ...\r\n"...)

// File is one file of the disk's root directory.
type File struct {
	// Name is the file's name as the computer shows it: letters, digits,
	// '.', '_' and '-', at most 100 characters, such as
	// "NanoKVM-Sensors-Setup-Windows.exe".
	Name string
	Data []byte
}

// names gives a file its directory entries' names: the 8.3 name every FAT
// entry has (an alias BASE~N.EXT made from the long one), and the VFAT
// entries that carry the long name, which come before it in the directory.
func names(name string, n int) (base, ext string, long []byte, err error) {
	if name == "" || len(name) > 100 || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return "", "", nil, fmt.Errorf("not a file name for the helper disk: %q", name)
	}
	for _, c := range name {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return "", "", nil, fmt.Errorf("not a file name for the helper disk: %q", name)
		}
	}
	stem := name
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		stem, ext = name[:dot], strings.ToUpper(name[dot+1:])
	}
	if len(ext) > 3 {
		ext = ext[:3]
	}
	stem = strings.ToUpper(strings.ReplaceAll(stem, ".", ""))
	if len(stem) > 6 {
		stem = stem[:6]
	}
	base = fmt.Sprintf("%s~%d", stem, n)
	if len(base) > 8 {
		return "", "", nil, fmt.Errorf("too many files for the alias of %q", name)
	}
	// The checksum of the 8.3 name ties the long entries to it.
	short := fmt.Sprintf("%-8s%-3s", base, ext)
	var sum byte
	for i := 0; i < 11; i++ {
		sum = sum>>1 | sum<<7
		sum += short[i]
	}
	// 13 UTF-16 units an entry: the name, one zero, then 0xFFFF; the last
	// part of the name comes first and carries 0x40.
	units := utf16.Encode([]rune(name))
	if len(units)%13 != 0 {
		units = append(units, 0)
		for len(units)%13 != 0 {
			units = append(units, 0xFFFF)
		}
	}
	count := len(units) / 13
	for seq := count; seq >= 1; seq-- {
		e := make([]byte, 32)
		e[0] = byte(seq)
		if seq == count {
			e[0] |= 0x40
		}
		e[11], e[13] = 0x0F, sum
		part := units[(seq-1)*13 : seq*13]
		for i, offset := range []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30} {
			binary.LittleEndian.PutUint16(e[offset:], part[i])
		}
		long = append(long, e...)
	}
	return base, ext, long, nil
}

func dirEntry(base, ext string, attr, nt byte, cluster uint16, size uint32) []byte {
	e := make([]byte, 32)
	copy(e, fmt.Sprintf("%-8s%-3s", base, ext))
	e[11], e[12] = attr, nt
	binary.LittleEndian.PutUint16(e[16:], fatDate) // created
	binary.LittleEndian.PutUint16(e[18:], fatDate) // accessed
	binary.LittleEndian.PutUint16(e[24:], fatDate) // written
	binary.LittleEndian.PutUint16(e[26:], cluster)
	binary.LittleEndian.PutUint32(e[28:], size)
	return e
}

// Build returns the image with the files, in their order.
func Build(files []File) ([]byte, error) {
	fat := []uint16{0xF00 | media, 0xFFF}
	root := dirEntry(label[:8], label[8:], attrLabel, 0, 0, 0)
	image := make([]byte, Size)
	next := 2
	seen := map[string]bool{}
	id := sha256.New()
	for n, f := range files {
		base, ext, long, err := names(f.Name, n+1)
		if err != nil {
			return nil, err
		}
		if seen[strings.ToLower(f.Name)] {
			return nil, fmt.Errorf("two files named %s", f.Name)
		}
		seen[strings.ToLower(f.Name)] = true
		count := (len(f.Data) + sectorsPerCluster*sector - 1) / (sectorsPerCluster * sector)
		if next-2+count > dataClusters {
			return nil, errors.New("the files do not fit on the helper disk")
		}
		first := 0
		if count > 0 {
			first = next
			for i := 1; i < count; i++ {
				fat = append(fat, uint16(first+i))
			}
			fat = append(fat, 0xFFF)
			copy(image[(dataSector+(first-2)*sectorsPerCluster)*sector:], f.Data)
			next += count
		}
		root = append(root, long...)
		root = append(root, dirEntry(base, ext, attrArchive, 0, uint16(first), uint32(len(f.Data)))...)
		if len(root) > rootEntries*32 {
			return nil, errors.New("too many files for the helper disk")
		}
		fmt.Fprintf(id, "%s %d\n", f.Name, len(f.Data))
		id.Write(f.Data)
	}

	// The boot sector. The volume id follows the contents, so the computer
	// sees a changed disk as another one.
	boot := image[:sector]
	copy(boot, []byte{0xEB, 0x3C, 0x90})
	copy(boot[3:], "NANOKVM ")
	binary.LittleEndian.PutUint16(boot[11:], sector)
	boot[13] = sectorsPerCluster
	binary.LittleEndian.PutUint16(boot[14:], reservedSectors)
	boot[16] = fats
	binary.LittleEndian.PutUint16(boot[17:], rootEntries)
	binary.LittleEndian.PutUint16(boot[19:], totalSectors)
	boot[21] = media
	binary.LittleEndian.PutUint16(boot[22:], sectorsPerFAT)
	binary.LittleEndian.PutUint16(boot[24:], 63)  // sectors per track
	binary.LittleEndian.PutUint16(boot[26:], 255) // heads
	boot[36], boot[38] = 0x80, 0x29
	copy(boot[39:43], id.Sum(nil)[:4])
	copy(boot[43:], label)
	copy(boot[54:], "FAT12   ")
	copy(boot[0x3E:], bootCode)
	boot[510], boot[511] = 0x55, 0xAA

	// FAT12: two entries in three bytes.
	table := make([]byte, sectorsPerFAT*sector)
	for i := 0; i < len(fat); i += 2 {
		a, b := fat[i], uint16(0)
		if i+1 < len(fat) {
			b = fat[i+1]
		}
		o := i / 2 * 3
		table[o], table[o+1], table[o+2] = byte(a), byte(a>>8&0x0F|b&0x0F<<4), byte(b>>4)
	}
	for n := 0; n < fats; n++ {
		copy(image[(reservedSectors+n*sectorsPerFAT)*sector:], table)
	}
	copy(image[(reservedSectors+fats*sectorsPerFAT)*sector:], root)
	return image, nil
}
