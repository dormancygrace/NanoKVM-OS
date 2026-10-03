package common

import (
	"bytes"
	"testing"
)

func TestPointerContainerEDIDByteOrder(t *testing.T) {
	id, err := parsePointerContainerID("2ca7b40c-7bd1-4f25-b573-a13a975ddc07\n")
	if err != nil || !bytes.Equal(id, []byte{0x2c, 0xa7, 0xb4, 0x0c, 0x7b, 0xd1, 0x4f, 0x25, 0xb5, 0x73, 0xa1, 0x3a, 0x97, 0x5d, 0xdc, 7}) {
		t.Fatalf("GUID %x %v", id, err)
	}
	for _, bad := range []string{"", "00000000-0000-0000-0000-000000000000", "2ca7b40c7bd14f25b573a13a975ddc07"} {
		if _, e := parsePointerContainerID(bad); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
func TestWindowsPointerEDIDPreservesAllTimingsInDenseProfile(t *testing.T) {
	edid := make([]byte, 256)
	copy(edid, []byte{0, 255, 255, 255, 255, 255, 255, 0})
	edid[126] = 1
	edid[54] = 3
	edid[55] = 1
	edid[75] = 0xff
	edid[93] = 0xfc
	edid[111] = 0xfd
	edid[128] = 2
	edid[129] = 3
	edid[130] = 18
	// Valid HDMI vendor block followed by an audio block, total 14 bytes.
	copy(edid[132:], []byte{0x65, 3, 12, 0, 0, 0, 0x27, 9, 7, 7, 0, 0, 0, 0})
	for i := 0; i < 6; i++ {
		edid[146+18*i] = byte(i + 1)
		edid[147+18*i] = 1
	}
	checksum := func(a []byte) {
		for o := 0; o < 256; o += 128 {
			var s byte
			for _, v := range a[o : o+127] {
				s += v
			}
			a[o+127] = -s
		}
	}
	checksum(edid)
	id, _ := parsePointerContainerID("2ca7b40c-7bd1-4f25-b573-a13a975ddc07")
	result, err := windowsPointerEDID(edid, id)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		want := edid[146+18*i : 164+18*i]
		found := false
		for j := 54; j <= 108; j += 18 {
			found = found || bytes.Equal(want, result[j:j+18])
		}
		for j := 128 + int(result[130]); j+18 <= 255; j += 18 {
			found = found || bytes.Equal(want, result[j:j+18])
		}
		if !found {
			t.Fatalf("lost timing %d", i)
		}
	}
	if !bytes.Equal(result[54:72], edid[54:72]) || !bytes.Equal(result[90:126], edid[90:126]) {
		t.Fatal("changed preferred timing, name or ranges")
	}
	if !bytes.Contains(result[132:128+int(result[130])], append([]byte{0x75, 0x5c, 0x12, 0xca, 3, 0x42}, id...)) {
		t.Fatal("missing desktop ContainerID")
	}
	for o := 0; o < 256; o += 128 {
		var s byte
		for _, v := range result[o : o+128] {
			s += v
		}
		if s != 0 {
			t.Fatal("bad checksum")
		}
	}
	again, e := windowsPointerEDID(result, id)
	if e != nil || !bytes.Equal(again, result) {
		t.Fatal("EDID decoration is not idempotent", e)
	}
	edid[20]++
	if _, e := windowsPointerEDID(edid, id); e == nil {
		t.Fatal("accepted corrupt EDID")
	}
}
