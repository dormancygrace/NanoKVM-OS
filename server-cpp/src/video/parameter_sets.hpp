#pragma once

// Parameter sets of key frames, rewritten before they reach a viewer.
//
// The SG2002 H.265 encoder in SmartP mode codes two temporal sub-layers: the IDR and every background ("virtual
// I") frame have TemporalId 0, all other frames TemporalId 1. Its VPS and SPS signal per-sub-layer ordering info
// with max_dec_pic_buffering_minus1[0] = 0, a one-picture DPB for sub-layer 0, although the background frame
// predicts from the long-term picture and so needs two. Chrome's hardware HEVC decoder rejects the first
// background frame; WebRTC then freezes and the Direct client resets its decoder and asks for an IDR each time.
// Signalling one set for all sub-layers (sub_layer_ordering_info_present_flag = 0, with the highest sub-layer's
// values) makes the parameter sets consistent; the slices are untouched.
//
// The encoders also write no VUI, so decoders guess how the YUV is coded: WebCodecs drew the limited-range BT.709
// picture of the HDMI receiver as full range (washed out). The SPS of each key frame gets a video_signal_type
// (limited range, BT.709 primaries, transfer and matrix); an SPS that has a VUI keeps its range and only gets what
// is missing.

#include "video/encoder.hpp"
#include "video/frame.hpp"

namespace nkos::video {


// Rewrites the parameter sets of an Annex B key frame in place: sets that shrink move the data start later; sets
// that grow (the colour description adds bytes) copy the frame into a new buffer with the same headroom.
void fix_parameter_sets(Codec codec, bool colour, Frame &frame);

} // namespace nkos::video
