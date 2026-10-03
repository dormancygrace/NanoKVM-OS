package webrtc

import (
	"bytes"
	"fmt"
	"testing"

	"NanoKVM-Server/service/stream"
	"github.com/pion/rtp/codecs"
)

func TestOwnedH264PayloaderMatchesPion(t *testing.T) {
	nal := func(header byte, size int) []byte {
		return append([]byte{header}, bytes.Repeat([]byte{0x55}, size)...)
	}
	annex := func(nals ...[]byte) []byte {
		var out []byte
		for i, nalu := range nals {
			if i%2 == 0 {
				out = append(out, 0, 0, 0, 1)
			} else {
				out = append(out, 0, 0, 1)
			}
			out = append(out, nalu...)
		}
		return out
	}
	var many []byte
	for i := 0; i < 40; i++ {
		many = append(many, annex(nal(0x41, i%9))...)
	}
	for _, disable := range []bool{false, true} {
		for _, mtu := range []uint16{0, 1, 2, 3, 4, 7, 8, 12, 20, 32, 600, 1216, 65535} {
			t.Run(fmt.Sprintf("DisableStapA%t/MTU%d", disable, mtu), func(t *testing.T) {
				owned := &ownedH264Payloader{DisableStapA: disable}
				pion := &codecs.H264Payloader{DisableStapA: disable}
				inputs := [][]byte{
					nil, {}, {0}, {0, 0}, {0, 0, 1}, {0, 0, 0, 1},
					nal(0x41, 0), nal(0xc5, 1), nal(0x65, 19), nal(0x65, 37),
					nal(0xe5, 2600), // Match Pion F/NRI treatment on malformed headers.
					annex(nil, nal(0x09, 2), nal(0x0c, 4), nil),
					annex(nal(0x67, 7)), // Parameter sets can arrive in separate calls.
					nil,
					annex(nal(0x68, 3)),
					annex(nal(0x09, 2), nal(0x06, 1), nal(0x65, 99)),
					annex(nal(0x67, 5), nal(0x67, 11), nal(0x68, 2), nal(0x41, 1)),
					annex(nal(0x67, 500), nal(0x68, 2), nal(0x41, 99)),
					annex(nal(0x41, 40), nil, nal(0x06, 1), nal(0x41, 100)),
					{0x55, 0, 0, 1, 0x41, 0x55, 0, 0, 0, 1},
					many,
				}
				var retained [][]byte
				var snapshots [][]byte
				for call, input := range inputs {
					want := pion.Payload(mtu, input)
					got := owned.Payload(mtu, input)
					if len(got) != len(want) {
						t.Fatalf("call %d packet count got=%d want=%d", call, len(got), len(want))
					}
					for i := range got {
						if !bytes.Equal(got[i], want[i]) {
							t.Fatalf("call %d packet %d differs: got=%x want=%x", call, i, got[i], want[i])
						}
						if len(got[i]) > int(mtu) {
							t.Fatalf("payload exceeds MTU: %d > %d", len(got[i]), mtu)
						}
						if cap(got[i]) != len(got[i]) {
							t.Fatal("payload capacity exposes adjacent packet storage")
						}
						retained = append(retained, got[i])
						snapshots = append(snapshots, bytes.Clone(got[i]))
					}
				}
				for i := range retained {
					if !bytes.Equal(retained[i], snapshots[i]) {
						t.Fatalf("retained payload %d changed after later calls", i)
					}
				}
			})
		}
	}
}

func TestOwnedH264PayloaderCopiesInputAndRetainedParameterSets(t *testing.T) {
	owned := &ownedH264Payloader{}
	pion := &codecs.H264Payloader{}
	sps := []byte{0, 0, 0, 1, 0x67, 0x42, 0, 0x1f, 0xe5}
	pps := []byte{0, 0, 1, 0x68, 0xce, 0x3c, 0x80}
	for _, input := range [][]byte{sps, pps} {
		want := pion.Payload(1216, bytes.Clone(input))
		got := owned.Payload(1216, input)
		if len(got) != 0 || len(want) != 0 {
			t.Fatal("parameter-only input unexpectedly emitted payload")
		}
		for i := range input {
			input[i] ^= 0xff
		}
	}
	frame := ownedPayloadFixture(stream.VideoCodecH264, false, 24<<10)
	want := pion.Payload(1216, frame)
	got := owned.Payload(1216, frame)
	if len(got) != len(want) {
		t.Fatalf("packet count got=%d want=%d", len(got), len(want))
	}
	snapshots := make([][]byte, len(got))
	for i := range got {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("parameter cache or payload %d changed with old input", i)
		}
		snapshots[i] = bytes.Clone(got[i])
	}
	for i := range frame {
		frame[i] ^= 0xff
	}
	owned.Payload(1216, ownedPayloadFixture(stream.VideoCodecH264, true, 192<<10))
	for i := range got {
		if !bytes.Equal(got[i], snapshots[i]) {
			t.Fatalf("payload %d changed after input reuse or a later frame", i)
		}
	}
}
