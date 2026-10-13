package webrtc

import (
	"bytes"
	"testing"
	"unsafe"

	"NanoKVM-Server/service/stream"
)

// The NACK responder keeps the packets it is given without copying them
// (nack.DisableCopy in h264.go). That is only correct while the packetizer
// never hands out memory that it or its caller writes again: every frame's
// payloads must be fresh, and independent of the encoder's input buffer.
func TestPacketizedPayloadsAreNeverReused(t *testing.T) {
	overlaps := func(a, b []byte) bool {
		if len(a) == 0 || len(b) == 0 {
			return false
		}
		a0, b0 := uintptr(unsafe.Pointer(&a[0])), uintptr(unsafe.Pointer(&b[0]))
		return a0 < b0+uintptr(len(b)) && b0 < a0+uintptr(len(a))
	}
	frames := map[stream.VideoCodec][][]byte{
		stream.VideoCodecH264: {
			append([]byte{0, 0, 0, 1, 0x67, 1, 2, 3, 0, 0, 0, 1, 0x68, 4, 5, 0, 0, 1, 0x65}, bytes.Repeat([]byte{7}, 5000)...),
			append([]byte{0, 0, 0, 1, 0x41}, bytes.Repeat([]byte{9}, 3000)...),
		},
		stream.VideoCodecH265: {
			appendH265AnnexB(nil, []byte{0x40, 1, 1}, []byte{0x42, 1, 2}, []byte{0x44, 1, 3},
				append([]byte{0x26, 1}, bytes.Repeat([]byte{7}, 5000)...)),
			appendH265AnnexB(nil, append([]byte{0x02, 1}, bytes.Repeat([]byte{9}, 3000)...)),
		},
	}
	for codec, aus := range frames {
		p := newAdaptiveVideoPacketizer(codec)
		var kept [][]byte
		var wants [][]byte
		input := make([]byte, 0, 8192) // one input buffer, reused per frame like the capture path
		for round := 0; round < 3; round++ {
			for _, au := range aus {
				input = append(input[:0], au...)
				packets := p.packetize(input, int64(round)*16666, 1200)
				if len(packets) == 0 {
					t.Fatalf("%s: no packets", codec)
				}
				for _, packet := range packets {
					if overlaps(packet.Payload, input[:cap(input)]) {
						t.Fatalf("%s: a payload aliases the input buffer", codec)
					}
					for _, old := range kept {
						if overlaps(packet.Payload, old) {
							t.Fatalf("%s: a payload reuses memory of an earlier packet", codec)
						}
					}
					kept = append(kept, packet.Payload)
					wants = append(wants, bytes.Clone(packet.Payload))
				}
				clear(input[:cap(input)]) // the caller reuses its buffer
			}
		}
		for i := range kept {
			if !bytes.Equal(kept[i], wants[i]) {
				t.Fatalf("%s: packet %d changed after later frames", codec, i)
			}
		}
	}
}
