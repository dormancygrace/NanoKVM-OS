package stream

import "bytes"

// The SG2002 H.265 encoder in SmartP mode codes two temporal sub-layers: the
// IDR and every background ("virtual I") frame have TemporalId 0, all other
// frames TemporalId 1. Its VPS and SPS signal per-sub-layer ordering info with
// max_dec_pic_buffering_minus1[0] = 0, a one-picture DPB for sub-layer 0,
// although the background frame (TemporalId 0) predicts from the long-term
// picture and so needs two. Chrome's hardware HEVC decoder rejects the first
// background frame (every gop frames); WebRTC then freezes and the Direct
// client resets its decoder and asks for an IDR each time. ffmpeg decodes it.
//
// Signalling one set for all sub-layers (sub_layer_ordering_info_present_flag
// = 0, with the highest sub-layer's values, which by definition hold for the
// whole stream) makes the parameter sets consistent; the slices are untouched.

const (
	hevcNALVPS = 32
	hevcNALSPS = 33
)

// fixParameterSets rewrites the parameter sets of an Annex B key frame: the
// H.265 VPS and SPS that signal ordering info per temporal sub-layer, and,
// when colour is set, the SPS of either codec to describe its colour coding
// (video_colour.go). storage is the frame allocation with headroom bytes
// before data (the Direct framing reuses it). Parameter sets that shrink are
// rewritten in place and both slices start later by the saved bytes; when
// they grow (the colour description adds a few bytes) the frame is copied
// into a new allocation with the same headroom. An access unit that needs no
// rewrite is returned unchanged.
func fixParameterSets(codec VideoCodec, colour bool, storage, data []byte, headroom int) ([]byte, []byte) {
	if codec == VideoCodecH264 {
		if !colour {
			return storage, data
		}
		return rewriteParameterSets(storage, data, headroom, isH264Slice, func(nal []byte) ([]byte, bool) {
			if len(nal) < 2 || nal[0]&31 != h264NALSPS {
				return nil, false
			}
			return h264SPSWithColour(nal)
		})
	}
	return rewriteParameterSets(storage, data, headroom, isHEVCSlice, func(nal []byte) ([]byte, bool) {
		if len(nal) < 3 {
			return nil, false
		}
		typ := int(nal[0]>>1) & 63
		if typ != hevcNALVPS && typ != hevcNALSPS {
			return nil, false
		}
		fixed, changed := hevcSingleOrderingSet(nal, typ)
		if !changed {
			fixed = nal
		}
		if colour && typ == hevcNALSPS {
			if described, ok := hevcSPSWithColour(fixed); ok {
				fixed, changed = described, true
			}
		}
		return fixed, changed
	})
}

// rewriteParameterSets replaces the NAL units before the first slice for
// which rewrite returns true.
func rewriteParameterSets(storage, data []byte, headroom int, isSlice func(byte) bool, rewrite func([]byte) ([]byte, bool)) ([]byte, []byte) {
	type replacement struct {
		start, end int
		nal        []byte
	}
	var replacements []replacement
	scanParameterSets(data, isSlice, func(start, end int) {
		if fixed, ok := rewrite(data[start:end]); ok {
			replacements = append(replacements, replacement{start, end, fixed})
		}
	})
	if len(replacements) == 0 {
		return storage, data
	}

	prefixEnd := replacements[len(replacements)-1].end
	prefix := make([]byte, 0, prefixEnd+64)
	last := 0
	for _, r := range replacements {
		prefix = append(prefix, data[last:r.start]...)
		prefix = append(prefix, r.nal...)
		last = r.end
	}
	prefix = append(prefix, data[last:prefixEnd]...)

	shift := prefixEnd - len(prefix)
	if shift < 0 {
		out := make([]byte, headroom+len(prefix)+len(data)-prefixEnd)
		copy(out[headroom:], prefix)
		copy(out[headroom+len(prefix):], data[prefixEnd:])
		return out, out[headroom:]
	}
	copy(data[shift:], prefix)
	contiguous := len(storage) == headroom+len(data) && &storage[headroom] == &data[0]
	data = data[shift:]
	if contiguous {
		storage = storage[shift:]
	}
	return storage, data
}

func isHEVCSlice(header byte) bool { return int(header>>1)&63 < hevcNALVPS }

// isH264Slice reports coded slices (types 1-5); SEI, SPS, PPS and access
// unit delimiters precede them.
func isH264Slice(header byte) bool {
	typ := header & 31
	return typ >= 1 && typ <= 5
}

// scanParameterSets calls visit with the bounds of each NAL unit (without
// start code) before the first slice; parameter sets precede it, so the slice
// data of a frame is never searched.
func scanParameterSets(data []byte, isSlice func(byte) bool, visit func(start, end int)) {
	startCode := []byte{0, 0, 1}
	i := bytes.Index(data, startCode)
	for i >= 0 {
		start := i + 3
		if start >= len(data) || isSlice(data[start]) {
			return
		}
		next := bytes.Index(data[start:], startCode)
		end := len(data)
		if next >= 0 {
			next += start
			end = next
			// A four-byte start code or trailing_zero_8bits belong to no NAL.
			for end > start && data[end-1] == 0 {
				end--
			}
		}
		visit(start, end)
		i = next
	}
}

// hevcSingleOrderingSet returns the NAL unit rewritten with
// sub_layer_ordering_info_present_flag = 0, or false when it already signals
// a single set, has one sub-layer or cannot be parsed.
func hevcSingleOrderingSet(nal []byte, typ int) ([]byte, bool) {
	rbsp := hevcUnescape(nal[2:])
	r := bitReader{data: rbsp}
	var maxSubLayersMinus1 uint32
	if typ == hevcNALVPS {
		r.skip(4 + 1 + 1 + 6) // id, base layer flags, max_layers_minus1
		maxSubLayersMinus1 = r.bits(3)
		r.skip(1 + 16) // temporal_id_nesting, reserved 0xffff
	} else {
		r.skip(4) // sps_video_parameter_set_id
		maxSubLayersMinus1 = r.bits(3)
		r.skip(1) // temporal_id_nesting
	}
	skipProfileTierLevel(&r, maxSubLayersMinus1)
	if typ == hevcNALSPS {
		r.ue() // sps_seq_parameter_set_id
		if r.ue() == 3 {
			r.skip(1) // separate_colour_plane_flag
		}
		r.ue() // pic_width_in_luma_samples
		r.ue() // pic_height_in_luma_samples
		if r.bits(1) == 1 {
			r.ue()
			r.ue()
			r.ue()
			r.ue()
		}
		r.ue() // bit_depth_luma_minus8
		r.ue() // bit_depth_chroma_minus8
		r.ue() // log2_max_pic_order_cnt_lsb_minus4
	}
	flagAt := r.pos
	if maxSubLayersMinus1 == 0 || r.bits(1) == 0 || r.failed {
		return nil, false
	}
	var dpb, reorder, latency uint32
	for i := uint32(0); i <= maxSubLayersMinus1; i++ {
		// The spec requires both to be non-decreasing; take the maxima so a
		// stream that breaks that rule is not given too small a DPB.
		dpb = max(dpb, r.ue())
		reorder = max(reorder, r.ue())
		latency = r.ue()
	}
	stop := lastSetBit(rbsp)
	if r.failed || stop < r.pos {
		return nil, false
	}

	var w bitWriter
	w.copyBits(rbsp, 0, flagAt)
	w.put(0, 1)
	w.ue(dpb)
	w.ue(reorder)
	w.ue(latency)
	w.copyBits(rbsp, r.pos, stop)
	w.put(1, 1) // rbsp_stop_one_bit
	out := append([]byte{nal[0], nal[1]}, hevcEscape(w.bytes())...)
	return out, true
}

func skipProfileTierLevel(r *bitReader, maxSubLayersMinus1 uint32) {
	r.skip(88 + 8) // general profile, tier and constraint flags; general_level_idc
	profilePresent := make([]bool, maxSubLayersMinus1)
	levelPresent := make([]bool, maxSubLayersMinus1)
	for i := range profilePresent {
		profilePresent[i] = r.bits(1) == 1
		levelPresent[i] = r.bits(1) == 1
	}
	if maxSubLayersMinus1 > 0 {
		r.skip(int(8-maxSubLayersMinus1) * 2) // reserved_zero_2bits
	}
	for i := range profilePresent {
		if profilePresent[i] {
			r.skip(88)
		}
		if levelPresent[i] {
			r.skip(8)
		}
	}
}

func hevcUnescape(b []byte) []byte {
	out := make([]byte, 0, len(b))
	zeros := 0
	for _, x := range b {
		if zeros >= 2 && x == 3 {
			zeros = 0
			continue
		}
		out = append(out, x)
		if x == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

func hevcEscape(b []byte) []byte {
	out := make([]byte, 0, len(b)+len(b)/64+1)
	zeros := 0
	for _, x := range b {
		if zeros >= 2 && x <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, x)
		if x == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// lastSetBit returns the bit position of rbsp_stop_one_bit, or -1.
func lastSetBit(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0 {
			for bit := 0; bit < 8; bit++ {
				if b[i]&(1<<bit) != 0 {
					return i*8 + 7 - bit
				}
			}
		}
	}
	return -1
}

type bitReader struct {
	data   []byte
	pos    int
	failed bool
}

func (r *bitReader) bit() uint32 {
	if r.pos >= len(r.data)*8 {
		r.failed = true
		return 0
	}
	v := uint32(r.data[r.pos/8]>>(7-r.pos%8)) & 1
	r.pos++
	return v
}

func (r *bitReader) bits(n int) uint32 {
	var v uint32
	for range n {
		v = v<<1 | r.bit()
	}
	return v
}

func (r *bitReader) skip(n int) {
	r.pos += n
	if r.pos > len(r.data)*8 {
		r.failed = true
	}
}

func (r *bitReader) ue() uint32 {
	zeros := 0
	for r.bit() == 0 {
		if r.failed || zeros == 31 {
			r.failed = true
			return 0
		}
		zeros++
	}
	return (1<<zeros - 1) + r.bits(zeros)
}

type bitWriter struct {
	data []byte
	n    int
}

func (w *bitWriter) put(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.data = append(w.data, 0)
		}
		if v>>i&1 == 1 {
			w.data[w.n/8] |= 1 << (7 - w.n%8)
		}
		w.n++
	}
}

func (w *bitWriter) ue(v uint32) {
	v++
	n := 0
	for x := v; x > 1; x >>= 1 {
		n++
	}
	w.put(0, n)
	w.put(v, n+1)
}

func (w *bitWriter) copyBits(src []byte, from, to int) {
	for i := from; i < to; i++ {
		w.put(uint32(src[i/8]>>(7-i%8))&1, 1)
	}
}

// bytes returns the written bits, zero-padded to a byte boundary.
func (w *bitWriter) bytes() []byte { return w.data }
