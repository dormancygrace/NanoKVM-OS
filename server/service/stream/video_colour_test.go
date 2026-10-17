package stream

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// SPS rewrites checked with ffmpeg: trace_headers of the rewritten streams
// differs from the original only in the VUI colour fields, the decoded
// frames are identical (-err_detect explode), and ffprobe reports
// tv/bt709/bt709/bt709.
const (
	// smartPFixedSPS with a VUI that only carries the signal type.
	smartPColourSPS = "420102016000000300000300000300000300960000a001402005a1653e49306b96f9a808080804"
	// SG2002 H.264 Main 4K, no VUI (venc dump from a NanoKVM).
	standH264SPS       = "674d4033a6803c0043e4"
	standH264ColourSPS = "674d4033a6803c0043e9a808080810"
	standH264PPS       = "68ee3880"
	h264IDRStub        = "6588840c0ffee0"
)

func TestSPSColourDescription(t *testing.T) {
	cases := []struct {
		name      string
		codec     VideoCodec
		sps, want string
	}{
		{"SG2002 H.265 without VUI", VideoCodecH265, smartPFixedSPS, smartPColourSPS},
		{"SG2002 H.264 without VUI", VideoCodecH264, standH264SPS, standH264ColourSPS},
		{
			"x264 High, VUI without signal type",
			VideoCodecH264,
			"6764000cacd941419f9f011000000300100000030320f1429960",
			"6764000cacd941419f9f016a020202800000030080000019078a14cb",
		},
		{
			// The range the encoder gave is kept; only the description is added.
			"x264 full range without colour description",
			VideoCodecH264,
			"6764000cacd941419f9f016c800000030080000019078a14cb",
			"6764000cacd941419f9f016e020202800000030080000019078a14cb",
		},
		{
			"x265 signal type without colour description",
			VideoCodecH265,
			"42010101600000030090000003000003003ca00a080b9f796566924caf016808000003000800000300c840",
			"42010101600000030090000003000003003ca00a080b9f796566924caf016a02020208000003000800000300c840",
		},
		{
			// smartPFixedSPS with four short-term RPS, three predicted from
			// the previous set; the third drops an entry with dPoc 0, so the
			// fourth codes flags for three pictures, not four.
			"H.265 inter-predicted RPS",
			VideoCodecH265,
			"420102016000000300000300000300000300960000a001402005a1653e49302b5aafb25bbfe4",
			"420102016000000300000300000300000300960000a001402005a1653e49302b5aafb25bbff35010101008",
		},
		{
			"x265 scaling lists enabled",
			VideoCodecH265,
			"42010101600000030090000003000003003ca00a080b9f796566924e5780b4040000030004000003006420",
			"42010101600000030090000003000003003ca00a080b9f796566924e5780b5010101040000030004000003006420",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := spsWithColour(t, tc.codec, tc.sps)
			if !ok || hex.EncodeToString(got) != tc.want {
				t.Fatalf("got %x %v\nwant %s", got, ok, tc.want)
			}
			if again, ok := spsWithColour(t, tc.codec, tc.want); ok {
				t.Fatalf("described SPS rewritten again: %x", again)
			}
		})
	}
}

func TestSPSColourDescriptionLeavesOtherSPSAlone(t *testing.T) {
	cases := []struct {
		name  string
		codec VideoCodec
		sps   string
	}{
		{"x264 already described", VideoCodecH264, "6764000cacd941419f9f016a040402800000030080000019078a14cb"},
		{"truncated H.264", VideoCodecH264, standH264SPS[:10]},
		{"truncated H.265", VideoCodecH265, smartPFixedSPS[:40]},
		{"H.264 scaling matrix", VideoCodecH264, "6764000cad" + "ffff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := spsWithColour(t, tc.codec, tc.sps); ok {
				t.Fatalf("rewritten: %x", got)
			}
		})
	}
}

func spsWithColour(t *testing.T, codec VideoCodec, sps string) ([]byte, bool) {
	t.Helper()
	nal, err := hex.DecodeString(sps)
	if err != nil {
		t.Fatal(err)
	}
	if codec == VideoCodecH264 {
		return h264SPSWithColour(nal)
	}
	return hevcSPSWithColour(nal)
}

func TestParameterSetsGrowIntoNewFrame(t *testing.T) {
	cases := []struct {
		name      string
		codec     VideoCodec
		in, want  []byte
		unchanged []byte
	}{
		{
			"H.265 SmartP key frame",
			VideoCodecH265,
			annexB(t, smartPVPS, smartPSPS, smartPPPS, idrSliceStub),
			annexB(t, smartPFixedVPS, smartPColourSPS, smartPPPS, idrSliceStub),
			annexB(t, smartPFixedVPS, smartPFixedSPS, smartPPPS, idrSliceStub),
		},
		{
			"H.264 key frame",
			VideoCodecH264,
			annexB(t, standH264SPS, standH264PPS, h264IDRStub),
			annexB(t, standH264ColourSPS, standH264PPS, h264IDRStub),
			annexB(t, standH264SPS, standH264PPS, h264IDRStub),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage, data := withHeadroom(bytes.Clone(tc.in))
			gotStorage, got := fixParameterSets(tc.codec, true, storage, data, videoHeadroom)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("access unit\n got %x\nwant %x", got, tc.want)
			}
			if len(gotStorage) != videoHeadroom+len(got) || &gotStorage[videoHeadroom] != &got[0] {
				t.Fatal("storage does not frame the data with the Direct headroom")
			}

			storage, data = withHeadroom(bytes.Clone(tc.in))
			_, got = fixParameterSets(tc.codec, false, storage, data, videoHeadroom)
			if !bytes.Equal(got, tc.unchanged) {
				t.Fatalf("without colour\n got %x\nwant %x", got, tc.unchanged)
			}
		})
	}
}

func TestH264ParameterSetScanStopsAtFirstSlice(t *testing.T) {
	// An SPS after the slice (here inside its payload) is not visited.
	au := annexB(t, "09f0", standH264SPS, standH264PPS, h264IDRStub, standH264SPS)
	storage, data := withHeadroom(au)
	_, got := fixParameterSets(VideoCodecH264, true, storage, data, videoHeadroom)
	want := annexB(t, "09f0", standH264ColourSPS, standH264PPS, h264IDRStub, standH264SPS)
	if !bytes.Equal(got, want) {
		t.Fatalf("access unit\n got %x\nwant %x", got, want)
	}
}
