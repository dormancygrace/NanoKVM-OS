package stream

import "os"

// The SG2002 encoders write no VUI, so H.264 and H.265 streams do not say how
// their YUV is coded and every decoder guesses. The HDMI receiver delivers
// limited-range (16-235) BT.709 video: Chrome's WebRTC decoders guessed
// right, WebCodecs drew it as full range (washed out), and recordings and
// other players are left to their own defaults. The SPS of each key frame is
// rewritten to say so in its VUI (video_signal_type: limited range, BT.709
// primaries, transfer and matrix); an SPS from another encoder that has a VUI
// keeps its range and only gets what is missing. NANOKVM_VIDEO_VUI=0 turns
// the rewrite off.

const (
	vuiVideoFormatUnspecified = 5
	colourBT709               = 1
)

var videoColourRewrite = os.Getenv("NANOKVM_VIDEO_VUI") != "0"

const h264NALSPS = 7

// writeVideoSignalType writes video_signal_type_present_flag = 1 and its
// fields; the layout is the same in H.264 and H.265.
func writeVideoSignalType(w *bitWriter) {
	w.put(1, 1)                         // video_signal_type_present_flag
	w.put(vuiVideoFormatUnspecified, 3) // video_format
	w.put(0, 1)                         // video_full_range_flag: 16-235
	writeColourDescription(w)
}

func writeColourDescription(w *bitWriter) {
	w.put(1, 1)           // colour_description_present_flag
	w.put(colourBT709, 8) // colour_primaries
	w.put(colourBT709, 8) // transfer_characteristics
	w.put(colourBT709, 8) // matrix_coefficients
}

// withColour rewrites rbsp (starting after the NAL header) so its VUI
// describes the colour coding. r has just read up to
// vui_parameters_present_flag; minimalVUI writes the rest of a VUI that only
// carries the signal type. An existing VUI keeps its range and video format:
// only a missing signal type or colour description is added.
func withColour(rbsp []byte, r *bitReader, minimalVUI func(*bitWriter)) ([]byte, bool) {
	stop := lastSetBit(rbsp)
	if r.failed || stop < r.pos {
		return nil, false
	}
	var w bitWriter
	at := r.pos
	if r.bits(1) == 0 { // vui_parameters_present_flag
		w.copyBits(rbsp, 0, at)
		w.put(1, 1)
		w.put(0, 1) // aspect_ratio_info_present_flag
		w.put(0, 1) // overscan_info_present_flag
		writeVideoSignalType(&w)
		minimalVUI(&w)
	} else {
		if r.bits(1) == 1 { // aspect_ratio_info_present_flag
			if r.bits(8) == 255 { // aspect_ratio_idc: EXTENDED_SAR
				r.skip(32)
			}
		}
		if r.bits(1) == 1 { // overscan_info_present_flag
			r.skip(1)
		}
		at = r.pos
		if r.bits(1) == 0 { // video_signal_type_present_flag
			w.copyBits(rbsp, 0, at)
			writeVideoSignalType(&w)
		} else {
			r.skip(3 + 1) // video_format, video_full_range_flag
			at = r.pos
			if r.bits(1) == 1 { // colour_description_present_flag
				return nil, false
			}
			w.copyBits(rbsp, 0, at)
			writeColourDescription(&w)
		}
	}
	if r.failed || stop < r.pos {
		return nil, false
	}
	w.copyBits(rbsp, r.pos, stop)
	w.put(1, 1) // rbsp_stop_one_bit
	return w.bytes(), true
}

// h264SPSWithColour returns the SPS NAL unit with the colour description, or
// false when it has one already or cannot be parsed (left unchanged).
func h264SPSWithColour(nal []byte) ([]byte, bool) {
	rbsp := hevcUnescape(nal[1:])
	r := bitReader{data: rbsp}
	profile := r.bits(8)
	r.skip(16) // constraint flags, level_idc
	r.ue()     // seq_parameter_set_id
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		if r.ue() == 3 { // chroma_format_idc
			r.skip(1) // separate_colour_plane_flag
		}
		r.ue()    // bit_depth_luma_minus8
		r.ue()    // bit_depth_chroma_minus8
		r.skip(1) // qpprime_y_zero_transform_bypass_flag
		if r.bits(1) == 1 {
			return nil, false // seq_scaling_matrix_present_flag: not parsed
		}
	}
	r.ue()          // log2_max_frame_num_minus4
	switch r.ue() { // pic_order_cnt_type
	case 0:
		r.ue() // log2_max_pic_order_cnt_lsb_minus4
	case 1:
		r.skip(1) // delta_pic_order_always_zero_flag
		r.se()    // offset_for_non_ref_pic
		r.se()    // offset_for_top_to_bottom_field
		for n := r.ue(); n > 0 && !r.failed; n-- {
			r.se() // offset_for_ref_frame
		}
	}
	r.ue()              // max_num_ref_frames
	r.skip(1)           // gaps_in_frame_num_value_allowed_flag
	r.ue()              // pic_width_in_mbs_minus1
	r.ue()              // pic_height_in_map_units_minus1
	if r.bits(1) == 0 { // frame_mbs_only_flag
		r.skip(1) // mb_adaptive_frame_field_flag
	}
	r.skip(1)           // direct_8x8_inference_flag
	if r.bits(1) == 1 { // frame_cropping_flag
		r.ue()
		r.ue()
		r.ue()
		r.ue()
	}
	out, ok := withColour(rbsp, &r, func(w *bitWriter) {
		w.put(0, 1) // chroma_loc_info_present_flag
		w.put(0, 1) // timing_info_present_flag
		w.put(0, 1) // nal_hrd_parameters_present_flag
		w.put(0, 1) // vcl_hrd_parameters_present_flag
		w.put(0, 1) // pic_struct_present_flag
		w.put(0, 1) // bitstream_restriction_flag
	})
	if !ok {
		return nil, false
	}
	return append([]byte{nal[0]}, hevcEscape(out)...), true
}

// hevcSPSWithColour is the H.265 counterpart of h264SPSWithColour.
func hevcSPSWithColour(nal []byte) ([]byte, bool) {
	rbsp := hevcUnescape(nal[2:])
	r := bitReader{data: rbsp}
	r.skip(4) // sps_video_parameter_set_id
	maxSubLayersMinus1 := r.bits(3)
	r.skip(1) // sps_temporal_id_nesting_flag
	skipProfileTierLevel(&r, maxSubLayersMinus1)
	r.ue()           // sps_seq_parameter_set_id
	if r.ue() == 3 { // chroma_format_idc
		r.skip(1)
	}
	r.ue()              // pic_width_in_luma_samples
	r.ue()              // pic_height_in_luma_samples
	if r.bits(1) == 1 { // conformance_window_flag
		r.ue()
		r.ue()
		r.ue()
		r.ue()
	}
	r.ue() // bit_depth_luma_minus8
	r.ue() // bit_depth_chroma_minus8
	log2MaxPOCLSB := int(r.ue()) + 4
	first := maxSubLayersMinus1
	if r.bits(1) == 1 { // sps_sub_layer_ordering_info_present_flag
		first = 0
	}
	for i := first; i <= maxSubLayersMinus1 && !r.failed; i++ {
		r.ue()
		r.ue()
		r.ue()
	}
	for range 6 { // coding block, transform block sizes and depths
		r.ue()
	}
	if scalingListEnabled := r.bits(1) == 1; scalingListEnabled && r.bits(1) == 1 {
		return nil, false // sps_scaling_list_data_present_flag: not parsed
	}
	r.skip(2)           // amp_enabled_flag, sample_adaptive_offset_enabled_flag
	if r.bits(1) == 1 { // pcm_enabled_flag
		r.skip(8)
		r.ue()
		r.ue()
		r.skip(1)
	}
	if !skipShortTermRefPicSets(&r, r.ue()) {
		return nil, false
	}
	if r.bits(1) == 1 { // long_term_ref_pics_present_flag
		for n := r.ue(); n > 0 && !r.failed; n-- {
			r.skip(log2MaxPOCLSB + 1)
		}
	}
	r.skip(2) // sps_temporal_mvp_enabled_flag, strong_intra_smoothing_enabled_flag
	out, ok := withColour(rbsp, &r, func(w *bitWriter) {
		w.put(0, 1) // chroma_loc_info_present_flag
		w.put(0, 1) // neutral_chroma_indication_flag
		w.put(0, 1) // field_seq_flag
		w.put(0, 1) // frame_field_info_present_flag
		w.put(0, 1) // default_display_window_flag
		w.put(0, 1) // vui_timing_info_present_flag
		w.put(0, 1) // bitstream_restriction_flag
	})
	if !ok {
		return nil, false
	}
	return append([]byte{nal[0], nal[1]}, hevcEscape(out)...), true
}

// skipShortTermRefPicSets parses num st_ref_pic_set() structures of an SPS.
// A set predicted from the previous one codes a flag pair per picture of
// that set, so the delta POCs of every set are derived as in H.265 (7-61,
// 7-62).
func skipShortTermRefPicSets(r *bitReader, num uint32) bool {
	if num > 64 {
		return false
	}
	type refPicSet struct{ s0, s1 []int32 } // negative, positive delta POCs
	sets := make([]refPicSet, 0, num)
	for idx := uint32(0); idx < num && !r.failed; idx++ {
		var set refPicSet
		if idx > 0 && r.bits(1) == 1 { // inter_ref_pic_set_prediction_flag
			sign := int32(r.bits(1))                   // delta_rps_sign
			deltaRps := (1 - 2*sign) * int32(r.ue()+1) // abs_delta_rps_minus1
			ref := sets[idx-1]
			n := len(ref.s0) + len(ref.s1)
			use := make([]bool, n+1)
			for j := range use {
				used := r.bits(1) == 1          // used_by_curr_pic_flag
				use[j] = used || r.bits(1) == 1 // use_delta_flag
			}
			for j := len(ref.s1) - 1; j >= 0; j-- {
				if d := ref.s1[j] + deltaRps; d < 0 && use[len(ref.s0)+j] {
					set.s0 = append(set.s0, d)
				}
			}
			if deltaRps < 0 && use[n] {
				set.s0 = append(set.s0, deltaRps)
			}
			for j, poc := range ref.s0 {
				if d := poc + deltaRps; d < 0 && use[j] {
					set.s0 = append(set.s0, d)
				}
			}
			for j := len(ref.s0) - 1; j >= 0; j-- {
				if d := ref.s0[j] + deltaRps; d > 0 && use[j] {
					set.s1 = append(set.s1, d)
				}
			}
			if deltaRps > 0 && use[n] {
				set.s1 = append(set.s1, deltaRps)
			}
			for j, poc := range ref.s1 {
				if d := poc + deltaRps; d > 0 && use[len(ref.s0)+j] {
					set.s1 = append(set.s1, d)
				}
			}
		} else {
			negative, positive := r.ue(), r.ue()
			if negative > 16 || positive > 16 {
				return false
			}
			poc := int32(0)
			for range negative {
				poc -= int32(r.ue() + 1) // delta_poc_s0_minus1
				r.skip(1)                // used_by_curr_pic_s0_flag
				set.s0 = append(set.s0, poc)
			}
			poc = 0
			for range positive {
				poc += int32(r.ue() + 1) // delta_poc_s1_minus1
				r.skip(1)                // used_by_curr_pic_s1_flag
				set.s1 = append(set.s1, poc)
			}
		}
		if len(set.s0)+len(set.s1) > 16 {
			return false
		}
		sets = append(sets, set)
	}
	return !r.failed
}

func (r *bitReader) se() int32 {
	v := r.ue()
	if v&1 == 1 {
		return int32(v+1) / 2
	}
	return -int32(v / 2)
}
