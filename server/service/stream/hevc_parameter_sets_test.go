package stream

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Parameter sets as the SG2002 encoder emits them in SmartP at 2560x1440
// (captured from the stand), and the rewrite that Chrome's hardware decoder
// accepted on the same capture (170 of 170 frames instead of an error at the
// first background frame).
const (
	smartPVPS      = "40010c02ffff016000000300000300000300000300960000f78120"
	smartPSPS      = "420102016000000300000300000300000300960000a001402005a165f7c9260d72de40"
	smartPPPS      = "4401e072b02640"
	smartPFixedVPS = "40010c02ffff0160000003000003000003000003009600003c09"
	smartPFixedSPS = "420102016000000300000300000300000300960000a001402005a1653e49306b96f2"
	idrSliceStub   = "2601afc0ffee"
	trailSliceStub = "0201d00ba5e5"
)

func annexB(t *testing.T, nals ...string) []byte {
	t.Helper()
	var out []byte
	for _, nal := range nals {
		b, err := hex.DecodeString(nal)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, 0, 0, 0, 1)
		out = append(out, b...)
	}
	return out
}

func withHeadroom(data []byte) ([]byte, []byte) {
	storage := make([]byte, videoHeadroom+len(data))
	copy(storage[videoHeadroom:], data)
	return storage, storage[videoHeadroom:]
}

func TestHEVCSubLayerOrderingSmartPKeyframe(t *testing.T) {
	storage, data := withHeadroom(annexB(t, smartPVPS, smartPSPS, smartPPPS, idrSliceStub))
	end := &data[len(data)-1]

	gotStorage, got := fixHEVCSubLayerOrdering(storage, data, videoHeadroom)

	want := annexB(t, smartPFixedVPS, smartPFixedSPS, smartPPPS, idrSliceStub)
	if !bytes.Equal(got, want) {
		t.Fatalf("access unit\n got %x\nwant %x", got, want)
	}
	if &got[len(got)-1] != end {
		t.Fatal("the access unit was copied instead of rewritten in place")
	}
	if len(gotStorage) != videoHeadroom+len(got) || &gotStorage[videoHeadroom] != &got[0] {
		t.Fatal("storage no longer frames the data with the Direct headroom")
	}
}

// x265 with temporal layers: a conformance window and VUI (1366x768, two
// sub-layers) and three sub-layers (3840x2160). The rewrites decode to the
// same frames as the originals in ffmpeg with -err_detect explode, and
// trace_headers shows one ordering set with the highest sub-layer's values.
func TestHEVCSubLayerOrderingOtherLayouts(t *testing.T) {
	cases := []struct {
		name               string
		vps, sps           string
		fixedVPS, fixedSPS string
	}{
		{
			"1366x768 two sub-layers",
			"40010c02ffff01600000030090000003000003007800009598acc048",
			"4201020160000003009000000300000300780000a002ac80301d7965662b349265780b4040000003004000000f02",
			"40010c02ffff0160000003009000000300000300780000159809",
			"4201020160000003009000000300000300780000a002ac80301d794566924caf016808000003000800000301e040",
		},
		{
			"3840x2160 three sub-layers",
			"40010c04ffff01600000030090000003000003009600009594aca5650240",
			"4201040160000003009000000300000300960000a001e020021c5965652b295952930bc05a020000030002000003003c10",
			"40010c04ffff0160000003009000000300000300960000159409",
			"4201040160000003009000000300000300960000a001e020021c5945654a4c2f016808000003000800000300f040",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage, data := withHeadroom(annexB(t, tc.vps, tc.sps, smartPPPS, idrSliceStub))
			_, got := fixHEVCSubLayerOrdering(storage, data, videoHeadroom)
			want := annexB(t, tc.fixedVPS, tc.fixedSPS, smartPPPS, idrSliceStub)
			if !bytes.Equal(got, want) {
				t.Fatalf("access unit\n got %x\nwant %x", got, want)
			}
		})
	}
}

func TestHEVCSubLayerOrderingLeavesOtherFramesAlone(t *testing.T) {
	cases := map[string][]byte{
		// One sub-layer (NormalP has one; this one is from x265).
		"single sub-layer": annexB(t,
			"40010c01ffff016000000300900000030000030078959809",
			"420101016000000300900000030000030078a002ac80301d796566924caf016808000003000800000301e040",
			smartPPPS, idrSliceStub),
		"already fixed": annexB(t, smartPFixedVPS, smartPFixedSPS, smartPPPS, idrSliceStub),
		"delta frame":   annexB(t, trailSliceStub),
		"no start code": {1, 2, 3},
		"truncated sps": annexB(t, smartPSPS[:24]),
	}
	for name, au := range cases {
		t.Run(name, func(t *testing.T) {
			storage, data := withHeadroom(au)
			gotStorage, got := fixHEVCSubLayerOrdering(storage, data, videoHeadroom)
			if &gotStorage[0] != &storage[0] || len(gotStorage) != len(storage) || &got[0] != &data[0] || len(got) != len(data) {
				t.Fatal("frame was changed")
			}
			if !bytes.Equal(got, au) {
				t.Fatalf("bytes changed: %x", got)
			}
		})
	}
}

func TestHEVCSubLayerOrderingSlicesAfterParameterSetsAreNotParsed(t *testing.T) {
	// A slice that contains a start code pattern after its header would be
	// split by a scan of the whole frame; only the leading NALs are visited.
	au := annexB(t, smartPVPS, smartPSPS, smartPPPS, idrSliceStub+"000001"+smartPSPS)
	storage, data := withHeadroom(au)
	_, got := fixHEVCSubLayerOrdering(storage, data, videoHeadroom)
	want := annexB(t, smartPFixedVPS, smartPFixedSPS, smartPPPS, idrSliceStub+"000001"+smartPSPS)
	if !bytes.Equal(got, want) {
		t.Fatalf("access unit\n got %x\nwant %x", got, want)
	}
}

func TestHEVCSubLayerOrderingWithoutFramingStorage(t *testing.T) {
	data := annexB(t, smartPVPS, smartPSPS, smartPPPS, idrSliceStub)
	other := make([]byte, 4)
	gotStorage, got := fixHEVCSubLayerOrdering(other, data, videoHeadroom)
	if &gotStorage[0] != &other[0] || len(gotStorage) != len(other) {
		t.Fatal("storage that does not frame the data was changed")
	}
	if want := annexB(t, smartPFixedVPS, smartPFixedSPS, smartPPPS, idrSliceStub); !bytes.Equal(got, want) {
		t.Fatalf("access unit\n got %x\nwant %x", got, want)
	}
}

func TestVideoSourceFixesH265KeyframeParameterSets(t *testing.T) {
	source := newVideoSource(func(EncoderConfig) ([]byte, []byte, int) { return nil, nil, 0 })
	h265 := DefaultEncoderConfig()
	h264 := h265
	h264.Codec = VideoCodecH264

	for _, tc := range []struct {
		config EncoderConfig
		result int
		fixed  bool
	}{
		{h265, keyFrameResult, true},
		{h265, 2, false},
		{h264, keyFrameResult, false},
	} {
		session := &videoSession{config: tc.config}
		subscription := &VideoSubscription{source: source, session: session, frames: make(chan VideoFrame, 1), done: make(chan struct{})}
		source.mutex.Lock()
		source.session = session
		source.subscribers = map[*VideoSubscription]struct{}{subscription: {}}
		source.refreshSnapshotLocked()
		source.mutex.Unlock()

		storage, data := withHeadroom(annexB(t, smartPVPS, smartPSPS, smartPPPS, idrSliceStub))
		source.publish(session, storage, data, tc.result, 0, 0)
		frame := <-subscription.frames
		want := annexB(t, smartPVPS, smartPSPS, smartPPPS, idrSliceStub)
		if tc.fixed {
			want = annexB(t, smartPFixedVPS, smartPFixedSPS, smartPPPS, idrSliceStub)
		}
		if !bytes.Equal(frame.Data, want) {
			t.Fatalf("%s result %d: got %x", tc.config.Codec, tc.result, frame.Data)
		}
		if len(frame.Storage) != videoHeadroom+len(frame.Data) || &frame.Storage[videoHeadroom] != &frame.Data[0] {
			t.Fatalf("%s result %d: storage does not frame the data", tc.config.Codec, tc.result)
		}
	}
}
