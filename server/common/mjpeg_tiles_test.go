package common

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// A minimal JPEG header: SOI, an APP0 segment, then SOF0 with the size.
func testJpeg(width, height int) []byte {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x04, 0x00, 0x00, 0xff, 0xc0, 0x00, 0x0b, 0x08}
	jpeg = binary.BigEndian.AppendUint16(jpeg, uint16(height))
	jpeg = binary.BigEndian.AppendUint16(jpeg, uint16(width))
	return append(jpeg, 0x01, 0x01, 0x11, 0x00, 0xff, 0xd9)
}

func TestJpegSizeReadsStartOfFrame(t *testing.T) {
	width, height, ok := JpegSize(testJpeg(1920, 1080))
	if !ok || width != 1920 || height != 1080 {
		t.Fatalf("JpegSize = %d x %d, %t", width, height, ok)
	}
	for _, bad := range [][]byte{nil, {0xff, 0xd8}, {0x00, 0x01, 0x02, 0x03}, testJpeg(1920, 1080)[:12]} {
		if _, _, ok := JpegSize(bad); ok {
			t.Fatalf("JpegSize(% x) succeeded", bad)
		}
	}
}

func TestWholeMessageWrapsTheJpeg(t *testing.T) {
	jpeg := testJpeg(1280, 720)
	message := MjpegTilesWholeMessage(jpeg)
	if !MjpegTilesMessageWhole(message) {
		t.Fatal("message is not whole")
	}
	want := []byte{1, MjpegTilesWhole, 1, 0, 0x00, 0x05, 0xd0, 0x02, 0, 0, 0, 0, 0x00, 0x05, 0xd0, 0x02}
	want = binary.LittleEndian.AppendUint32(want, uint32(len(jpeg)))
	want = append(want, jpeg...)
	if !bytes.Equal(message, want) {
		t.Fatalf("message\n% x\nwant\n% x", message, want)
	}
	if MjpegTilesWholeMessage([]byte("not a jpeg")) != nil {
		t.Fatal("a picture without a size was wrapped")
	}
}
